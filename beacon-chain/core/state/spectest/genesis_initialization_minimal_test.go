package spectest

import (
	"io/ioutil"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/gogo/protobuf/proto"
	"github.com/prysmaticlabs/go-ssz"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/helpers"
	"github.com/prysmaticlabs/prysm/beacon-chain/core/state"
	ethpb "github.com/prysmaticlabs/prysm/proto/eth/v1alpha1"
	"github.com/prysmaticlabs/prysm/shared/params"
	"github.com/prysmaticlabs/prysm/shared/params/spectest"
	"github.com/prysmaticlabs/prysm/shared/testutil"
)

func TestGenesisInitializationMinimal(t *testing.T) {
	t.Skip("This test suite requires --define ssz=minimal to be provided and there isn't a great way to do that without breaking //... See https://github.com/prysmaticlabs/prysm/issues/3066")
	filepath, err := bazel.Runfile("tests/genesis/initialization/genesis_initialization_minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	file, err := ioutil.ReadFile(filepath)
	if err != nil {
		t.Fatalf("Could not load file %v", err)
	}

	s := &GenesisInitializationTest{}
	if err := testutil.UnmarshalYaml(file, s); err != nil {
		t.Fatalf("Failed to Unmarshal: %v", err)
	}

	if err := spectest.SetConfig(s.Config); err != nil {
		t.Fatal(err)
	}

	if len(s.TestCases) == 0 {
		t.Fatal("No tests!")
	}

	for _, tt := range s.TestCases {
		t.Run(tt.Description, func(t *testing.T) {
			helpers.ClearAllCaches()
			deposits := tt.Deposits
			dataLeaves := make([]*ethpb.Deposit_Data, len(deposits))
			for i := range deposits {
				dataLeaves[i] = deposits[i].Data
			}
			depositRoot, err := ssz.HashTreeRootWithCapacity(dataLeaves, 1<<params.BeaconConfig().DepositContractTreeDepth)
			if err != nil {
				t.Fatal(err)
			}
			eth1Data := &ethpb.Eth1Data{
				DepositRoot:  depositRoot[:],
				DepositCount: uint64(len(deposits)),
				BlockHash:    tt.Eth1BlockHash,
			}

			genesisState, err := state.GenesisBeaconState(deposits, tt.Eth1Timestamp, eth1Data)
			if err != nil {
				t.Fatal(err)
			}

			if !proto.Equal(genesisState, tt.State) {
				t.Error("States are not equal")
			}
		})
	}
}
