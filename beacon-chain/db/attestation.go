package db

import (
	"context"

	"github.com/boltdb/bolt"
	"github.com/gogo/protobuf/proto"
	"github.com/pkg/errors"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/hashutil"
	"go.opencensus.io/trace"
)

// SaveAttestation puts the attestation record into the beacon chain db.
func (db *BeaconDB) SaveAttestation(ctx context.Context, attestation *ethpb.Attestation) error {
	ctx, span := trace.StartSpan(ctx, "beaconDB.SaveAttestation")
	defer span.End()

	encodedAtt, err := proto.Marshal(attestation)
	if err != nil {
		return err
	}
	hash := hashutil.Hash(encodedAtt)

	return db.batch(func(tx *bolt.Tx) error {
		a := tx.Bucket(attestationBucket)

		return a.Put(hash[:], encodedAtt)
	})
}

// DeleteAttestation deletes the attestation record into the beacon chain db.
func (db *BeaconDB) DeleteAttestation(attestation *ethpb.Attestation) error {
	hash, err := hashutil.HashProto(attestation)
	if err != nil {
		return err
	}

	return db.batch(func(tx *bolt.Tx) error {
		a := tx.Bucket(attestationBucket)
		return a.Delete(hash[:])
	})
}

// Attestation retrieves an attestation record from the db using its hash.
func (db *BeaconDB) Attestation(hash [32]byte) (*ethpb.Attestation, error) {
	var attestation *ethpb.Attestation
	err := db.view(func(tx *bolt.Tx) error {
		a := tx.Bucket(attestationBucket)

		enc := a.Get(hash[:])
		if enc == nil {
			return nil
		}

		var err error
		attestation, err = createAttestation(enc)
		return err
	})

	return attestation, err
}

// Attestations retrieves all the attestation records from the db.
// These are the attestations that have not been seen on the beacon chain.
func (db *BeaconDB) Attestations() ([]*ethpb.Attestation, error) {
	var attestations []*ethpb.Attestation
	err := db.view(func(tx *bolt.Tx) error {
		a := tx.Bucket(attestationBucket)

		if err := a.ForEach(func(k, v []byte) error {
			attestation, err := createAttestation(v)
			if err != nil {
				return err
			}
			attestations = append(attestations, attestation)
			return nil
		}); err != nil {
			return err
		}
		return nil
	})

	return attestations, err
}

// HasAttestation checks if the attestation exists.
func (db *BeaconDB) HasAttestation(hash [32]byte) bool {
	exists := false
	// #nosec G104
	db.view(func(tx *bolt.Tx) error {
		a := tx.Bucket(attestationBucket)

		exists = a.Get(hash[:]) != nil
		return nil
	})
	return exists
}

func createAttestation(enc []byte) (*ethpb.Attestation, error) {
	protoAttestation := &ethpb.Attestation{}
	if err := proto.Unmarshal(enc, protoAttestation); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal encoding")
	}
	return protoAttestation, nil
}

func createAttestationTarget(enc []byte) (*pb.AttestationTarget, error) {
	protoAttTgt := &pb.AttestationTarget{}
	if err := proto.Unmarshal(enc, protoAttTgt); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal encoding")
	}
	return protoAttTgt, nil
}
