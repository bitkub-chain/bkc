package utils

import (
	"errors"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
)

// NewValidator creates new validator
func NewValidator(address common.Address, votingPower uint64) *ctypes.Validator {
	return &ctypes.Validator{
		Address:     address,
		VotingPower: votingPower,
	}
}

func SortByVotingPower(a []ctypes.Validator) []ctypes.Validator {
	sort.SliceStable(a, func(i, j int) bool {
		return a[i].VotingPower > a[j].VotingPower
	})
	return a
}

func ParseValidatorsAndPower(validatorsBytes []byte) ([]*ctypes.Validator, error) {
	if len(validatorsBytes)%40 != 0 {
		return nil, errors.New("invalid validators bytes")
	}

	result := make([]*ctypes.Validator, len(validatorsBytes)/40)
	for i := 0; i < len(validatorsBytes); i += 40 {
		address := make([]byte, 20)
		power := make([]byte, 20)

		copy(address, validatorsBytes[i:i+20])
		copy(power, validatorsBytes[i+20:i+40])

		result[i/40] = NewValidator(common.BytesToAddress(address), big.NewInt(0).SetBytes(power).Uint64())
	}
	return result, nil
}

func ParseValidators(validatorsBytes []byte) ([]common.Address, error) {
	if len(validatorsBytes)%40 != 0 {
		return nil, errors.New("invalid validators bytes")
	}

	result := make([]common.Address, len(validatorsBytes)/40)
	for i := 0; i < len(validatorsBytes); i += 40 {
		address := make([]byte, 20)
		copy(address, validatorsBytes[i:i+20])
		result[i/40] = common.BytesToAddress(address)
	}

	return result, nil
}
