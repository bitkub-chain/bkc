package clique

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
)

// Contract Client for calling proof-of-stake smart contract on bkc

//go:generate mockgen -source=./contract_client.go -destination=./mock/contract_client_mock.go -package=mock
type ContractClient interface {

	// Set default signer for contract client
	SetSigner(signer types.Signer)

	// Inject config and things in to a client
	Inject(val common.Address, signTxFn ctypes.SignerTxFn)

	// Send slash transaction
	Slash(contract common.Address, spoiledVal common.Address, chain consensus.ChainHeaderReader, state *state.StateDB, header *types.Header, cx core.ChainContext,
		txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, currentSpan *big.Int) error

	// Call for a current span number
	GetCurrentSpan(ctx context.Context, header *types.Header) (*big.Int, error)

	// Send distribute reward transaction
	DistributeToValidator(contract common.Address, amount *big.Int, validator common.Address,
		state *state.StateDB, header *types.Header, chain core.ChainContext,
		txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool) error

	// Send commit span transaction
	CommitSpan(val common.Address, state *state.StateDB, header *types.Header, chain core.ChainContext,
		txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, validatorBytes []byte) error

	// Call is signer slashed
	IsSlashed(contract common.Address, chain consensus.ChainHeaderReader, signer common.Address, span *big.Int, header *types.Header) (bool, error)

	// Call for  current commited validators
	GetCurrentValidators(headerHash common.Hash, blockNumber *big.Int) ([]*ctypes.Validator, *ctypes.SystemContracts, error)

	// Call for eligible validators
	GetEligibleValidators(headerHash common.Hash, blockNumber uint64) ([]*ctypes.Validator, error)
}
