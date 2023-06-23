package ctypes

import (
	"math/big"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// SignerFn hashes and signs the data to be signed by a backing account.
type SignerFn func(signer accounts.Account, mimeType string, message []byte) ([]byte, error)
type SignerTxFn func(accounts.Account, *types.Transaction, *big.Int) (*types.Transaction, error)

type SystemContracts struct {
	StakeManager common.Address `json:"stakeManager"`
	SlashManager common.Address `json:"slashManager"`
	OfficialNode common.Address `json:"officialNode"`
}

// Validator represets Volatile state for each Validator
type Validator struct {
	Address     common.Address `json:"signer"`
	VotingPower uint64         `json:"power"`
	// ProposerPriority int64          `json:"accum"`
}

// MinimalVal is the minimal validator representation
// Used to send validator information to bor validator contract
type MinimalVal struct {
	Signer      common.Address `json:"signer"`
	VotingPower uint64         `json:"power"`
}

func (v *Validator) HeaderBytes() []byte {
	result := make([]byte, 40)
	copy(result[:20], v.Address.Bytes())
	copy(result[20:], v.PowerBytes())
	return result
}

// PowerBytes return power bytes
func (v *Validator) PowerBytes() []byte {
	powerBytes := big.NewInt(0).SetUint64(v.VotingPower).Bytes()
	result := make([]byte, 20)
	copy(result[20-len(powerBytes):], powerBytes)
	return result
}

// MinimalVal returns block number of last validator update
func (v *Validator) MinimalVal() MinimalVal {
	return MinimalVal{
		Signer:      v.Address,
		VotingPower: uint64(v.VotingPower),
	}
}
