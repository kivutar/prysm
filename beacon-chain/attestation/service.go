// Package attestation defines the life-cycle and status of single and aggregated attestation.
package attestation

import (
	"context"
	"fmt"
	"sync"

	"github.com/gogo/protobuf/proto"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/db"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/bytesutil"
	"github.com/prysmaticlabs/prysm/shared/event"
	handler "github.com/prysmaticlabs/prysm/shared/messagehandler"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/sirupsen/logrus"
)

var log = logrus.WithField("prefix", "attestation")

// TargetHandler provides an interface for fetching latest attestation targets
// and updating attestations in batches.
type TargetHandler interface {
	LatestAttestationTarget(state *pb.BeaconState, validatorIndex uint64) (*pb.AttestationTarget, error)
	BatchUpdateLatestAttestation(ctx context.Context, atts []*ethpb.Attestation) error
}

type attestationStore struct {
	sync.RWMutex
	m map[[48]byte]*ethpb.Attestation
}

// Service represents a service that handles the internal
// logic of managing single and aggregated attestation.
type Service struct {
	ctx          context.Context
	cancel       context.CancelFunc
	beaconDB     *db.BeaconDB
	incomingFeed *event.Feed
	incomingChan chan *ethpb.Attestation
	// store is the mapping of individual
	// validator's public key to it's latest attestation.
	store              attestationStore
	pooledAttestations []*ethpb.Attestation
	poolLimit          int
}

// Config options for the service.
type Config struct {
	BeaconDB *db.BeaconDB
}

// NewAttestationService instantiates a new service instance that will
// be registered into a running beacon node.
func NewAttestationService(ctx context.Context, cfg *Config) *Service {
	ctx, cancel := context.WithCancel(ctx)
	return &Service{
		ctx:                ctx,
		cancel:             cancel,
		beaconDB:           cfg.BeaconDB,
		incomingFeed:       new(event.Feed),
		incomingChan:       make(chan *ethpb.Attestation, params.BeaconConfig().DefaultBufferSize),
		store:              attestationStore{m: make(map[[48]byte]*ethpb.Attestation)},
		pooledAttestations: make([]*ethpb.Attestation, 0, 1),
		poolLimit:          1,
	}
}

// Start an attestation service's main event loop.
func (a *Service) Start() {
	log.Info("Starting service")
	go a.attestationPool()
}

// Stop the Attestation service's main event loop and associated goroutines.
func (a *Service) Stop() error {
	defer a.cancel()
	log.Info("Stopping service")
	return nil
}

// Status always returns nil.
// TODO(#1201): Add service health checks.
func (a *Service) Status() error {
	return nil
}

// IncomingAttestationFeed returns a feed that any service can send incoming p2p attestations into.
// The attestation service will subscribe to this feed in order to relay incoming attestations.
func (a *Service) IncomingAttestationFeed() *event.Feed {
	return a.incomingFeed
}

// LatestAttestationTarget returns the target block that the validator index attested to,
// the highest slotNumber attestation in attestation pool gets returned.
//
// Spec pseudocode definition:
//	Let `get_latest_attestation_target(store: Store, validator_index: ValidatorIndex) ->
//		BeaconBlock` be the target block in the attestation
//		`get_latest_attestation(store, validator_index)`.
func (a *Service) LatestAttestationTarget(beaconState *pb.BeaconState, index uint64) (*pb.AttestationTarget, error) {
	if index >= uint64(len(beaconState.Validators)) {
		return nil, fmt.Errorf("invalid validator index %d", index)
	}
	validator := beaconState.Validators[index]

	pubKey := bytesutil.ToBytes48(validator.PublicKey)
	a.store.RLock()
	defer a.store.RUnlock()
	if _, exists := a.store.m[pubKey]; !exists {
		return nil, nil
	}

	attestation := a.store.m[pubKey]
	if attestation == nil {
		return nil, nil
	}
	targetRoot := bytesutil.ToBytes32(attestation.Data.BeaconBlockRoot)
	if !a.beaconDB.HasBlock(targetRoot) {
		return nil, nil
	}

	return a.beaconDB.AttestationTarget(targetRoot)
}

// attestationPool takes an newly received attestation from sync service
// and updates attestation pool.
func (a *Service) attestationPool() {
	incomingSub := a.incomingFeed.Subscribe(a.incomingChan)
	defer incomingSub.Unsubscribe()
	for {
		select {
		case <-a.ctx.Done():
			log.Debug("Attestation pool closed, exiting goroutine")
			return
		// Listen for a newly received incoming attestation from the sync service.
		case attestations := <-a.incomingChan:
			handler.SafelyHandleMessage(a.ctx, a.handleAttestation, attestations)
		}
	}
}

func (a *Service) handleAttestation(ctx context.Context, msg proto.Message) error {
	attestation := msg.(*ethpb.Attestation)
	a.pooledAttestations = append(a.pooledAttestations, attestation)
	if len(a.pooledAttestations) > a.poolLimit {
		if err := a.BatchUpdateLatestAttestation(ctx, a.pooledAttestations); err != nil {
			return err
		}
		state, err := a.beaconDB.HeadState(ctx)
		if err != nil {
			return err
		}

		// This sets the pool limit, once the old pool is cleared out. It does by using the number of active
		// validators per slot as an estimate. The active indices here are not used in the actual processing
		// of attestations.
		count, err := helpers.ActiveValidatorCount(state, helpers.CurrentEpoch(state))
		if err != nil {
			return err
		}
		attPerSlot := count / params.BeaconConfig().SlotsPerEpoch
		// we only set the limit at 70% of the calculated amount to be safe so that relevant attestations
		// arent carried over to the next batch.
		a.poolLimit = int(attPerSlot) * 7 / 10
		if a.poolLimit == 0 {
			a.poolLimit++
		}
		attestationPoolLimit.Set(float64(a.poolLimit))
		a.pooledAttestations = make([]*ethpb.Attestation, 0, a.poolLimit)
	}
	attestationPoolSize.Set(float64(len(a.pooledAttestations)))
	return nil
}

// UpdateLatestAttestation inputs an new attestation and checks whether
// the attesters who submitted this attestation with the higher slot number
// have been noted in the attestation pool. If not, it updates the
// attestation pool with attester's public key to attestation.
func (a *Service) UpdateLatestAttestation(ctx context.Context, attestation *ethpb.Attestation) error {
	totalAttestationSeen.Inc()

	// Potential improvement, instead of getting the state,
	// we could get a mapping of validator index to public key.
	beaconState, err := a.beaconDB.HeadState(ctx)
	if err != nil {
		return err
	}
	return a.updateAttestation(beaconState, attestation)
}

// BatchUpdateLatestAttestation updates multiple attestations and adds them into the attestation store
// if they are valid.
func (a *Service) BatchUpdateLatestAttestation(ctx context.Context, attestations []*ethpb.Attestation) error {

	if attestations == nil {
		return nil
	}
	// Potential improvement, instead of getting the state,
	// we could get a mapping of validator index to public key.
	beaconState, err := a.beaconDB.HeadState(ctx)
	if err != nil {
		return err
	}

	for _, attestation := range attestations {
		if err := a.updateAttestation(beaconState, attestation); err != nil {
			log.Error(err)
		}
	}
	return nil
}

// InsertAttestationIntoStore locks the store, inserts the attestation, then
// unlocks the store again. This method may be used by external services
// in testing to populate the attestation store.
func (a *Service) InsertAttestationIntoStore(pubkey [48]byte, att *ethpb.Attestation) {
	a.store.Lock()
	defer a.store.Unlock()
	a.store.m[pubkey] = att
}

func (a *Service) updateAttestation(beaconState *pb.BeaconState, attestation *ethpb.Attestation) error {
	totalAttestationSeen.Inc()

	committee, err := helpers.CrosslinkCommittee(beaconState, helpers.CurrentEpoch(beaconState), attestation.Data.Crosslink.Shard)
	if err != nil {
		return err
	}

	log.WithFields(logrus.Fields{
		"attestationTargetEpoch": attestation.Data.Target.Epoch,
		"attestationShard":       attestation.Data.Crosslink.Shard,
		"committeesList":         committee,
		"lengthOfCommittees":     len(committee),
	}).Debug("Updating latest attestation")

	// Check each bit of participation bitfield to find out which
	// attester has submitted new attestation.
	// This is has O(n) run time and could be optimized down the line.
	for i := uint64(0); i < attestation.AggregationBits.Len(); i++ {
		if !attestation.AggregationBits.BitAt(i) {
			continue
		}

		if i >= uint64(len(committee)) {
			// This should never happen.
			log.Warnf("bitfield points to an invalid index in the committee: bitfield %08b", attestation.AggregationBits)
			return nil
		}

		if int(committee[i]) >= len(beaconState.Validators) {
			// This should never happen.
			log.Warnf("index doesn't exist in validator registry: index %d", committee[i])
			return nil
		}

		// If the attestation came from this attester. We use the slot committee to find the
		// validator's actual index.
		pubkey := bytesutil.ToBytes48(beaconState.Validators[committee[i]].PublicKey)
		attTargetBoundarySlot := attestation.Data.Target.Epoch * params.BeaconConfig().SlotsPerEpoch
		currentAttestationSlot := uint64(0)
		a.store.Lock()
		defer a.store.Unlock()
		if _, exists := a.store.m[pubkey]; exists {
			currentAttestationSlot = attTargetBoundarySlot
		}
		// If the attestation is newer than this attester's one in pool.
		if attTargetBoundarySlot > currentAttestationSlot {
			a.store.m[pubkey] = attestation

			log.WithFields(
				logrus.Fields{
					"attTargetBoundarySlot": attTargetBoundarySlot,
					"sourceEpoch":           attestation.Data.Source.Epoch,
				},
			).Debug("Attestation store updated")
		}
	}
	return nil
}
