package blocks

import (
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	pb "github.com/prysmaticlabs/prysm/proto/beacon/p2p/v1"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/sirupsen/logrus"
)

func init() {
	logrus.SetLevel(logrus.DebugLevel)
}

type mockDB struct {
	hasBlock bool
}

func (f *mockDB) HasBlock(h [32]byte) bool {
	return f.hasBlock
}

type mockPOWClient struct {
	blockExists bool
}

func (m *mockPOWClient) BlockByHash(ctx context.Context, hash common.Hash) (*gethTypes.Block, error) {
	if m.blockExists {
		return &gethTypes.Block{}, nil
	}
	return nil, nil
}

func TestIsValidBlock_NoParent(t *testing.T) {
	beaconState := &pb.BeaconState{}

	ctx := context.Background()

	db := &mockDB{}
	powClient := &mockPOWClient{}

	beaconState.Slot = 3

	block := &ethpb.BeaconBlock{
		Slot: 4,
	}

	genesisTime := time.Unix(0, 0)

	db.hasBlock = false

	if err := IsValidBlock(ctx, beaconState, block,
		db.HasBlock, powClient.BlockByHash, genesisTime); err == nil {
		t.Fatal("block is valid despite not having a parent")
	}
}

func TestIsValidBlock_InvalidSlot(t *testing.T) {
	beaconState := &pb.BeaconState{}

	ctx := context.Background()

	db := &mockDB{}
	powClient := &mockPOWClient{}

	beaconState.Slot = 3

	block := &ethpb.BeaconBlock{
		Slot: 4,
	}

	genesisTime := time.Unix(0, 0)

	block.Slot = 3
	db.hasBlock = true

	beaconState.Eth1Data = &ethpb.Eth1Data{
		DepositRoot: []byte{2},
		BlockHash:   []byte{3},
	}
	if err := IsValidBlock(ctx, beaconState, block,
		db.HasBlock, powClient.BlockByHash, genesisTime); err == nil {
		t.Fatalf("block is valid despite having an invalid slot %d", block.Slot)
	}
}

func TestIsValidBlock_InvalidPoWReference(t *testing.T) {
	beaconState := &pb.BeaconState{}

	ctx := context.Background()

	db := &mockDB{}
	powClient := &mockPOWClient{}

	beaconState.Slot = 3

	block := &ethpb.BeaconBlock{
		Slot: 4,
	}

	genesisTime := time.Unix(0, 0)

	db.hasBlock = true
	block.Slot = 4
	powClient.blockExists = false
	beaconState.Eth1Data = &ethpb.Eth1Data{
		DepositRoot: []byte{2},
		BlockHash:   []byte{3},
	}

	if err := IsValidBlock(ctx, beaconState, block,
		db.HasBlock, powClient.BlockByHash, genesisTime); err == nil {
		t.Fatalf("block is valid despite having an invalid pow reference block")
	}

}
func TestIsValidBlock_InvalidGenesis(t *testing.T) {
	beaconState := &pb.BeaconState{}

	ctx := context.Background()

	db := &mockDB{}
	db.hasBlock = true

	powClient := &mockPOWClient{}
	powClient.blockExists = false

	beaconState.Slot = 3
	beaconState.Eth1Data = &ethpb.Eth1Data{
		DepositRoot: []byte{2},
		BlockHash:   []byte{3},
	}

	genesisTime := time.Unix(0, 0)
	block := &ethpb.BeaconBlock{
		Slot: 4,
	}

	invalidTime := time.Now().AddDate(1, 2, 3)

	if err := IsValidBlock(ctx, beaconState, block,
		db.HasBlock, powClient.BlockByHash, genesisTime); err == nil {
		t.Fatalf("block is valid despite having an invalid genesis time %v", invalidTime)
	}

}

func TestIsValidBlock_GoodBlock(t *testing.T) {
	beaconState := &pb.BeaconState{}
	ctx := context.Background()

	db := &mockDB{}
	db.hasBlock = true

	powClient := &mockPOWClient{}
	powClient.blockExists = true

	beaconState.Slot = 3
	beaconState.Eth1Data = &ethpb.Eth1Data{
		DepositRoot: []byte{2},
		BlockHash:   []byte{3},
	}

	genesisTime := time.Unix(0, 0)

	block := &ethpb.BeaconBlock{
		Slot: 4,
	}

	if err := IsValidBlock(ctx, beaconState, block,
		db.HasBlock, powClient.BlockByHash, genesisTime); err != nil {
		t.Fatal(err)
	}
}
