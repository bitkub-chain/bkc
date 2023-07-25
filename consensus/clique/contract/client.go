package contract

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/internal/ethapi"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
)

type ContractClient struct {
	stakeManagerABI abi.ABI
	slashManagerABI abi.ABI
	validatorSetABI abi.ABI
	config          *params.ChainConfig // Consensus engine configuration parameters
	signer          types.Signer
	val             common.Address
	signTxFn        ctypes.SignerTxFn
	ethAPI          EthAPI
}

func New(config *params.ChainConfig, ethAPI *ethapi.PublicBlockChainAPI) (*ContractClient, error) {
	vABI, err := abi.JSON(strings.NewReader(validatorSetABI))
	if err != nil {
		return &ContractClient{}, err
	}
	sABI, err := abi.JSON(strings.NewReader(stakeManageABI))
	if err != nil {
		return &ContractClient{}, err
	}
	slABI, err := abi.JSON(strings.NewReader(slashABI))
	if err != nil {
		return &ContractClient{}, err
	}

	return &ContractClient{
		stakeManagerABI: sABI,
		slashManagerABI: slABI,
		validatorSetABI: vABI,
		ethAPI:          ethAPI,
		config:          config,
	}, nil
}

// This function should be called in consensus intialization (clique.New)
func (cc *ContractClient) SetSigner(signer types.Signer) {
	cc.signer = signer
}

// Initialize function, should be called after consensus engine are selected
// and account has been authorized
func (cc *ContractClient) Inject(val common.Address, signTxFn ctypes.SignerTxFn) {
	cc.val = val
	cc.signTxFn = signTxFn
}

func (cc *ContractClient) Slash(contract common.Address, spoiledVal common.Address, chain consensus.ChainHeaderReader, state *state.StateDB, header *types.Header, cx core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, currentSpan *big.Int) error {
	method := "slash"
	// get packed data
	data, err := cc.slashManagerABI.Pack(method,
		spoiledVal,
		currentSpan,
	)
	if err != nil {
		log.Error("Unable to pack tx for slash", "error", err)
		return err
	}
	// get system message
	msg := getSystemMessage(header.Coinbase, contract, data, common.Big0)
	// apply message
	return cc.applyTransaction(msg, state, header, cx, txs, receipts, receivedTxs, usedGas, mining)
}

func (cc *ContractClient) GetCurrentSpan(ctx context.Context, header *types.Header) (*big.Int, error) {
	blockNr := rpc.BlockNumberOrHashWithHash(header.ParentHash, false)
	method := "currentSpanNumber"
	// get packed data
	data, err := cc.validatorSetABI.Pack(method)
	if err != nil {
		log.Error("Unable to pack tx for deposit", "error", err)
		return nil, err
	}

	msgData := (hexutil.Bytes)(data)
	toAddress := cc.getValidatorContract(header.Number)
	gas := (hexutil.Uint64)(uint64(math.MaxUint64 / 2))
	result, err := cc.ethAPI.Call(ctx, ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &toAddress,
		Data: &msgData,
	}, blockNr, nil)
	if err != nil {
		return nil, err
	}

	var ret0 *big.Int
	if err := cc.validatorSetABI.UnpackIntoInterface(&ret0, method, result); err != nil {
		return nil, err
	}
	return ret0, nil
}

func (cc *ContractClient) DistributeToValidator(contract common.Address, amount *big.Int, validator common.Address,
	state *state.StateDB, header *types.Header, chain core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool) error {
	method := "distributeReward"
	// get packed data
	data, err := cc.stakeManagerABI.Pack(method)
	if err != nil {
		log.Error("Unable to pack tx for deposit", "error", err)
		return err
	}
	// get system message
	msg := getSystemMessage(header.Coinbase, contract, data, amount)
	// apply message
	return cc.applyTransaction(msg, state, header, chain, txs, receipts, receivedTxs, usedGas, mining)
}

func (cc *ContractClient) CommitSpan(val common.Address, state *state.StateDB, header *types.Header, chain core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, validatorBytes []byte) error {
	method := "commitSpan"
	// get packed data
	data, err := cc.validatorSetABI.Pack(method,
		validatorBytes,
	)
	if err != nil {
		log.Error("Unable to pack tx for commitspan", "error", err)
		return err
	}
	validatorContract := cc.getValidatorContract(header.Number)
	// get system message
	msg := getSystemMessage(header.Coinbase, validatorContract, data, common.Big0)
	// apply message
	return cc.applyTransaction(msg, state, header, chain, txs, receipts, receivedTxs, usedGas, mining)
}

func (cc *ContractClient) IsSlashed(contract common.Address, chain consensus.ChainHeaderReader, signer common.Address, span *big.Int, header *types.Header) (bool, error) {
	blockNr := rpc.BlockNumberOrHashWithHash(header.ParentHash, false)

	method := "isSignerSlashed"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cancel when we are finished consuming integers

	// get packed data
	data, err := cc.slashManagerABI.Pack(
		method,
		signer,
		span,
	)

	if err != nil {
		log.Error("Unable to pack tx for isSignerSlashed", "error", err)
		return false, err
	}

	// call
	msgData := (hexutil.Bytes)(data)
	toAddress := contract
	gas := (hexutil.Uint64)(uint64(math.MaxUint64 / 2))
	result, err := cc.ethAPI.Call(ctx, ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &toAddress,
		Data: &msgData,
	}, blockNr, nil)
	if err != nil {
		return false, err
	}
	var out bool
	if err := cc.slashManagerABI.UnpackIntoInterface(&out, method, result); err != nil {
		return false, err
	}
	return out, nil
}

func (cc *ContractClient) GetCurrentValidators(headerHash common.Hash, blockNumber *big.Int) ([]*ctypes.Validator, *ctypes.SystemContracts, error) {
	// block
	blockNr := rpc.BlockNumberOrHashWithHash(headerHash, false)

	method := "getValidators"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cancel when we are finished consuming integers

	// get packed data
	data, err := cc.validatorSetABI.Pack(
		method,
		blockNumber,
	)
	if err != nil {
		log.Error("Unable to pack tx for getValidators", "error", err)
		return nil, nil, err
	}

	// call
	msgData := (hexutil.Bytes)(data)
	toAddress := cc.getValidatorContract(blockNumber)
	gas := (hexutil.Uint64)(uint64(math.MaxUint64 / 2))
	result, err := cc.ethAPI.Call(ctx, ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &toAddress,
		Data: &msgData,
	}, blockNr, nil)
	if err != nil {
		return nil, nil, err
	}

	var (
		ret0 = new([]common.Address)
		ret1 = new([]*big.Int)
		ret2 = new([3]common.Address)
	)
	out := &[]interface{}{
		ret0,
		ret1,
		ret2,
	}

	if err := cc.validatorSetABI.UnpackIntoInterface(out, method, result); err != nil {
		return nil, nil, err
	}

	valz := make([]*ctypes.Validator, len(*ret0))
	for i, a := range *ret0 {
		valz[i] = &ctypes.Validator{
			Address:     a,
			VotingPower: (*ret1)[i].Uint64(),
		}
	}
	ca := &ctypes.SystemContracts{
		StakeManager: (*ret2)[0],
		SlashManager: (*ret2)[1],
		OfficialNode: (*ret2)[2],
	}
	return valz, ca, nil
}

// GetCurrentValidators get current validators
func (cc *ContractClient) GetEligibleValidators(headerHash common.Hash, blockNumber uint64) ([]*ctypes.Validator, error) {
	blockNr := rpc.BlockNumberOrHashWithHash(headerHash, false)

	method := "getEligibleValidators"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// get packed data
	data, err := cc.validatorSetABI.Pack(
		method,
	)
	if err != nil {
		log.Error("Unable to pack tx for getValidator", "error", err)
		return nil, err
	}

	// call
	msgData := (hexutil.Bytes)(data)
	toAddress := cc.getValidatorContract(big.NewInt(int64(blockNumber)))
	gas := (hexutil.Uint64)(uint64(math.MaxUint64 / 2))
	result, err := cc.ethAPI.Call(ctx, ethapi.TransactionArgs{
		Gas:  &gas,
		To:   &toAddress,
		Data: &msgData,
	}, blockNr, nil)
	if err != nil {
		return nil, err
	}

	var ret0 = new([]struct {
		Address     common.Address
		VotingPower *big.Int
	})

	if err := cc.validatorSetABI.UnpackIntoInterface(ret0, method, result); err != nil {
		return nil, err
	}
	valz := make([]*ctypes.Validator, len(*ret0))
	for i, a := range *ret0 {
		valz[i] = &ctypes.Validator{
			Address:     a.Address,
			VotingPower: new(big.Int).Div(a.VotingPower, new(big.Int).SetInt64(int64(math.Pow(10, 18)))).Uint64(),
		}
	}

	return valz, nil
}

func (cc *ContractClient) getValidatorContract(number *big.Int) common.Address {
	validatorContract := cc.config.Clique.ValidatorContract
	if cc.config.ChaophrayaBangkokBlock != nil && cc.config.IsChaophrayaBangkok(number) {
		validatorContract = cc.config.Clique.ValidatorContractV2
	}
	return validatorContract
}

// Transaction handler functions vvv

// get system message
func getSystemMessage(from, toAddress common.Address, data []byte, value *big.Int) callmsg {
	return callmsg{
		ethereum.CallMsg{
			From:     from,
			Gas:      math.MaxUint64 / 2,
			GasPrice: big.NewInt(0),
			Value:    value,
			To:       &toAddress,
			Data:     data,
		},
	}
}

func (cc *ContractClient) applyTransaction(
	msg callmsg,
	state *state.StateDB,
	header *types.Header,
	chainContext core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt,
	receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool,
) (err error) {
	nonce := state.GetNonce(msg.From())
	expectedTx := types.NewTransaction(nonce, *msg.To(), msg.Value(), msg.Gas(), msg.GasPrice(), msg.Data())
	expectedHash := cc.signer.Hash(expectedTx)
	if msg.From() == cc.val && mining {
		expectedTx, err = cc.signTxFn(accounts.Account{Address: msg.From()}, expectedTx, cc.config.ChainID)
		if err != nil {
			return err
		}
	} else {
		if receivedTxs == nil || len(*receivedTxs) == 0 || (*receivedTxs)[0] == nil {
			return errors.New("supposed to get a actual transaction, but get none")
		}
		actualTx := (*receivedTxs)[0]
		if !bytes.Equal(cc.signer.Hash(actualTx).Bytes(), expectedHash.Bytes()) {
			return fmt.Errorf("expected tx hash %v, get %v, nonce %d, to %s, value %s, gas %d, gasPrice %s, data %s", expectedHash.String(), actualTx.Hash().String(),
				expectedTx.Nonce(),
				expectedTx.To().String(),
				expectedTx.Value().String(),
				expectedTx.Gas(),
				expectedTx.GasPrice().String(),
				hex.EncodeToString(expectedTx.Data()),
			)
		}
		expectedTx = actualTx
		// move to next
		*receivedTxs = (*receivedTxs)[1:]
	}
	state.Prepare(expectedTx.Hash(), len(*txs))
	gasUsed, err := applyMessage(msg, state, header, cc.config, chainContext)
	if err != nil {
		return err
	}
	*txs = append(*txs, expectedTx)
	var root []byte
	if cc.config.IsByzantium(header.Number) {
		state.Finalise(true)
	} else {
		root = state.IntermediateRoot(cc.config.IsEIP158(header.Number)).Bytes()
	}
	*usedGas += gasUsed
	receipt := types.NewReceipt(root, false, *usedGas)
	receipt.TxHash = expectedTx.Hash()
	receipt.GasUsed = gasUsed

	// Set the receipt logs and create a bloom for filtering
	receipt.Logs = state.GetLogs(expectedTx.Hash(), header.Hash())
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})
	receipt.BlockHash = header.Hash()
	receipt.BlockNumber = header.Number
	receipt.TransactionIndex = uint(state.TxIndex())
	*receipts = append(*receipts, receipt)
	state.SetNonce(msg.From(), nonce+1)
	return nil
}

// apply message
func applyMessage(
	msg callmsg,
	state *state.StateDB,
	header *types.Header,
	chainConfig *params.ChainConfig,
	chainContext core.ChainContext,
) (uint64, error) {
	// Create a new context to be used in the EVM environment
	context := core.NewEVMBlockContext(header, chainContext, nil)
	// Create a new environment which holds all relevant information
	// about the transaction and calling mechanisms.

	vmenv := vm.NewEVM(context, vm.TxContext{Origin: msg.From(), GasPrice: big.NewInt(0)}, state, chainConfig, vm.Config{})
	// Apply the transaction to the current state (included in the env)
	ret, returnGas, err := vmenv.Call(
		vm.AccountRef(msg.From()),
		*msg.To(),
		msg.Data(),
		msg.Gas(),
		msg.Value(),
	)
	if err != nil {
		log.Error("apply message failed", "msg", hex.EncodeToString(ret), "err", err)
	}
	return msg.Gas() - returnGas, err
}

// callmsg implements core.Message to allow passing it as a transaction simulator.
type callmsg struct {
	ethereum.CallMsg
}

func (m callmsg) From() common.Address { return m.CallMsg.From }
func (m callmsg) Nonce() uint64        { return 0 }
func (m callmsg) CheckNonce() bool     { return false }
func (m callmsg) To() *common.Address  { return m.CallMsg.To }
func (m callmsg) GasPrice() *big.Int   { return m.CallMsg.GasPrice }
func (m callmsg) Gas() uint64          { return m.CallMsg.Gas }
func (m callmsg) Value() *big.Int      { return m.CallMsg.Value }
func (m callmsg) Data() []byte         { return m.CallMsg.Data }
