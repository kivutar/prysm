package blockchain

import (
	"context"
	"testing"

	"github.com/gogo/protobuf/proto"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	"github.com/prysmaticlabs/prysm/beacon-chain/internal"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/testutil"
	logTest "github.com/sirupsen/logrus/hooks/test"
)

type mockAttestationHandler struct {
	targets map[uint64]*pb.AttestationTarget
}

func (m *mockAttestationHandler) LatestAttestationTarget(beaconState *pb.BeaconState, idx uint64) (*pb.AttestationTarget, error) {
	return m.targets[idx], nil
}

func (m *mockAttestationHandler) BatchUpdateLatestAttestation(ctx context.Context, atts []*ethpb.Attestation) error {
	return nil
}

func TestApplyForkChoice_ChainSplitReorg(t *testing.T) {
	// TODO(#2307): Fix test once v0.6 is merged.
	t.Skip()
	hook := logTest.NewGlobal()
	beaconDB := internal.SetupDB(t)
	defer internal.TeardownDB(t, beaconDB)

	ctx := context.Background()
	deposits, _ := testutil.SetupInitialDeposits(t, 100)
	justifiedState, err := state.GenesisBeaconState(deposits, 0, &ethpb.Eth1Data{})
	if err != nil {
		t.Fatalf("Can't generate genesis state: %v", err)
	}
	justifiedState.StateRoots = make([][]byte, params.BeaconConfig().HistoricalRootsLimit)
	justifiedState.LatestBlockHeader = &ethpb.BeaconBlockHeader{
		StateRoot: []byte{},
	}

	chainService := setupBeaconChain(t, beaconDB, nil)

	// Construct a forked chain that looks as follows:
	//    /------B1 ----B3 ----- B5 (current head)
	// B0 --B2 -------------B4
	blocks, roots := constructForkedChain(t, justifiedState)

	// We then setup a canonical chain of the following blocks:
	// B0->B1->B3->B5.
	if err := chainService.beaconDB.SaveBlock(blocks[0]); err != nil {
		t.Fatal(err)
	}
	if err := chainService.beaconDB.SaveJustifiedState(justifiedState); err != nil {
		t.Fatal(err)
	}
	if err := chainService.beaconDB.SaveJustifiedBlock(blocks[0]); err != nil {
		t.Fatal(err)
	}
	if err := chainService.beaconDB.UpdateChainHead(ctx, blocks[0], justifiedState); err != nil {
		t.Fatal(err)
	}
	canonicalBlockIndices := []int{1, 3, 5}
	postState := proto.Clone(justifiedState).(*pb.BeaconState)
	for _, canonicalIndex := range canonicalBlockIndices {
		postState, err = chainService.AdvanceState(ctx, postState, blocks[canonicalIndex])
		if err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveBlock(blocks[canonicalIndex]); err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.UpdateChainHead(ctx, blocks[canonicalIndex], postState); err != nil {
			t.Fatal(err)
		}
	}

	chainHead, err := chainService.beaconDB.ChainHead()
	if err != nil {
		t.Fatal(err)
	}
	if chainHead.Slot != justifiedState.Slot+5 {
		t.Errorf(
			"Expected chain head with slot %d, received %d",
			justifiedState.Slot+5,
			chainHead.Slot,
		)
	}

	// We then save forked blocks and their historical states (but do not update chain head).
	// The fork is from B0->B2->B4.
	forkedBlockIndices := []int{2, 4}
	forkState := proto.Clone(justifiedState).(*pb.BeaconState)
	for _, forkIndex := range forkedBlockIndices {
		forkState, err = chainService.AdvanceState(ctx, forkState, blocks[forkIndex])
		if err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveBlock(blocks[forkIndex]); err != nil {
			t.Fatal(err)
		}
		if err := chainService.beaconDB.SaveHistoricalState(ctx, forkState, roots[forkIndex]); err != nil {
			t.Fatal(err)
		}
	}

	// Give the block from the forked chain, B4, the most votes.
	voteTargets := make(map[uint64]*pb.AttestationTarget)
	voteTargets[0] = &pb.AttestationTarget{
		Slot:            blocks[5].Slot,
		BeaconBlockRoot: roots[5][:],
		ParentRoot:      blocks[5].ParentRoot,
	}
	for i := 1; i < len(deposits); i++ {
		voteTargets[uint64(i)] = &pb.AttestationTarget{
			Slot:            blocks[4].Slot,
			BeaconBlockRoot: roots[4][:],
			ParentRoot:      blocks[4].ParentRoot,
		}
	}
	attHandler := &mockAttestationHandler{
		targets: voteTargets,
	}
	chainService.attsService = attHandler

	block4State, err := chainService.beaconDB.HistoricalStateFromSlot(ctx, blocks[4].Slot, roots[4])
	if err != nil {
		t.Fatal(err)
	}
	// Applying the fork choice rule should reorg to B4 successfully.
	if err := chainService.ApplyForkChoiceRule(ctx, blocks[4], block4State); err != nil {
		t.Fatal(err)
	}

	newHead, err := chainService.beaconDB.ChainHead()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(newHead, blocks[4]) {
		t.Errorf(
			"Expected chain head %v, received %v",
			blocks[4],
			newHead,
		)
	}
	want := "Reorg happened"
	testutil.AssertLogsContain(t, hook, want)
}

func constructForkedChain(t *testing.T, beaconState *pb.BeaconState) ([]*ethpb.BeaconBlock, [][32]byte) {
	// Construct the following chain:
	//    /------B1 ----B3 ----- B5 (current head)
	// B0 --B2 -------------B4
	blocks := make([]*ethpb.BeaconBlock, 6)
	roots := make([][32]byte, 6)
	var err error
	blocks[0] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot,
		ParentRoot: []byte{'A'},
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[0], err = ssz.SigningRoot(blocks[0])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}

	blocks[1] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot + 2,
		ParentRoot: roots[0][:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[1], err = ssz.SigningRoot(blocks[1])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}

	blocks[2] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot + 1,
		ParentRoot: roots[0][:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[2], err = ssz.SigningRoot(blocks[2])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}

	blocks[3] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot + 3,
		ParentRoot: roots[1][:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[3], err = ssz.SigningRoot(blocks[3])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}

	blocks[4] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot + 4,
		ParentRoot: roots[2][:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[4], err = ssz.SigningRoot(blocks[4])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}

	blocks[5] = &ethpb.BeaconBlock{
		Slot:       beaconState.Slot + 5,
		ParentRoot: roots[3][:],
		Body: &ethpb.BeaconBlockBody{
			Eth1Data: &ethpb.Eth1Data{},
		},
	}
	roots[5], err = ssz.SigningRoot(blocks[5])
	if err != nil {
		t.Fatalf("Could not hash block: %v", err)
	}
	return blocks, roots
}
