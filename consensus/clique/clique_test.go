// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package clique

import (
	"bytes"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
	"github.com/ethereum/go-ethereum/consensus/clique/mock"
	"github.com/ethereum/go-ethereum/consensus/clique/test"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/golang/mock/gomock"
)

// This test case is a repro of an annoying bug that took us forever to catch.
// In Clique PoA networks (Rinkeby, Görli, etc), consecutive blocks might have
// the same state root (no block subsidy, empty block). If a node crashes, the
// chain ends up losing the recent state and needs to regenerate it from blocks
// already in the database. The bug was that processing the block *prior* to an
// empty one **also completes** the empty one, ending up in a known-block error.
func TestReimportMirroredState(t *testing.T) {
	t.Helper()

	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)
	// Initialize a Clique chain with a single signer
	var (
		db     = rawdb.NewMemoryDatabase()
		key, _ = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		addr   = crypto.PubkeyToAddress(key.PublicKey)
		engine = New(params.AllCliqueProtocolChanges, db, nil, mockContractClient)
		signer = new(types.HomesteadSigner)
	)
	genspec := &core.Genesis{
		ExtraData: make([]byte, extraVanity+common.AddressLength+extraSeal),
		Alloc: map[common.Address]core.GenesisAccount{
			addr: {Balance: big.NewInt(10000000000000000)},
		},
		BaseFee: big.NewInt(params.InitialBaseFee),
	}
	copy(genspec.ExtraData[extraVanity:], addr[:])
	genesis := genspec.MustCommit(db)

	// Generate a batch of blocks, each properly signed
	chain, _ := core.NewBlockChain(db, nil, params.AllCliqueProtocolChanges, engine, vm.Config{}, nil, nil)
	defer chain.Stop()

	blocks, _ := core.GenerateChain(params.AllCliqueProtocolChanges, genesis, engine, db, 3, func(i int, block *core.BlockGen) {
		// The chain maker doesn't have access to a chain, so the difficulty will be
		// lets unset (nil). Set it here to the correct value.
		block.SetDifficulty(diffInTurn)

		// We want to simulate an empty middle block, having the same state as the
		// first one. The last is needs a state change again to force a reorg.
		if i != 1 {
			tx, err := types.SignTx(types.NewTransaction(block.TxNonce(addr), common.Address{0x00}, new(big.Int), params.TxGas, block.BaseFee(), nil), signer, key)
			if err != nil {
				panic(err)
			}
			block.AddTxWithChain(chain, tx)
		}
	})
	for i, block := range blocks {
		header := block.Header()
		if i > 0 {
			header.ParentHash = blocks[i-1].Hash()
		}
		header.Extra = make([]byte, extraVanity+extraSeal)
		header.Difficulty = diffInTurn

		sig, _ := crypto.Sign(SealHash(header).Bytes(), key)
		copy(header.Extra[len(header.Extra)-extraSeal:], sig)
		blocks[i] = block.WithSeal(header)
	}
	// Insert the first two blocks and make sure the chain is valid
	db = rawdb.NewMemoryDatabase()
	genspec.MustCommit(db)

	chain, _ = core.NewBlockChain(db, nil, params.AllCliqueProtocolChanges, engine, vm.Config{}, nil, nil)
	defer chain.Stop()

	if _, err := chain.InsertChain(blocks[:2]); err != nil {
		t.Fatalf("failed to insert initial blocks: %v", err)
	}
	if head := chain.CurrentBlock().NumberU64(); head != 2 {
		t.Fatalf("chain head mismatch: have %d, want %d", head, 2)
	}

	// Simulate a crash by creating a new chain on top of the database, without
	// flushing the dirty states out. Insert the last block, triggering a sidechain
	// reimport.
	chain, _ = core.NewBlockChain(db, nil, params.AllCliqueProtocolChanges, engine, vm.Config{}, nil, nil)
	defer chain.Stop()

	if _, err := chain.InsertChain(blocks[2:]); err != nil {
		t.Fatalf("failed to insert final block: %v", err)
	}
	if head := chain.CurrentBlock().NumberU64(); head != 3 {
		t.Fatalf("chain head mismatch: have %d, want %d", head, 3)
	}
}

func TestSealHash(t *testing.T) {
	have := SealHash(&types.Header{
		Difficulty: new(big.Int),
		Number:     new(big.Int),
		Extra:      make([]byte, 32+65),
		BaseFee:    new(big.Int),
	})
	want := common.HexToHash("0xbd3d1fa43fbc4c5bfcc91b179ec92e2861df3654de60468beb908ff805359e8f")
	if have != want {
		t.Errorf("have %x, want %x", have, want)
	}
}

func TestCommitSpan(t *testing.T) {

	t.Helper()

	var (
		setPoSValidatorAtBlock = 49
		seedBlockNumber        = 71
		accountRegistry        = test.NewAccountRegistry() // Account Registry is used for storing account keys
	)
	var posBlock uint64
	var spanSize uint64

	// create coinbase account from string label
	accountRegistry.Add("coinbase")
	// get coinbase testing account
	coinbase := accountRegistry.Get("coinbase")

	// new mock controller
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// mock up all instances
	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockEthAPI := mock.NewMockEthAPI(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)

	db := rawdb.NewMemoryDatabase()

	// Load genesis from json file
	genspec := test.NewDefaultGenesis()

	// Set posBlock from config
	posBlock = genspec.Config.PoSBlock.Uint64()

	// Set spanSize from config
	spanSize = genspec.Config.Clique.Span

	// Set coinbase address
	genspec.ExtraData = make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(genspec.ExtraData[extraVanity:], coinbase.Address[:])

	// Genspec must be commited to the genesis block
	genspec.MustCommit(db)

	// Sign function, use for signing block
	signFn := func(account accounts.Account, s string, data []byte) ([]byte, error) {
		return crypto.Sign(crypto.Keccak256(data), coinbase.Key)
	}

	// Iinitialize Clique
	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	c := New(genspec.Config, db, mockEthAPI, mockContractClient)

	// Mock the inject method before authorize
	mockContractClient.EXPECT().Inject(
		gomock.Any(),
		gomock.Any(),
	).Times(1)
	c.Authorize(coinbase.Address, signFn, nil)

	// Create test chain
	testChain, err := test.NewTestChain(genspec.Config, c, db, signFn, coinbase)
	if err != nil {
		t.Fatal(err)
	}

	// Roll to number posBlock - 2
	err = testChain.Roll(t, setPoSValidatorAtBlock-1)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare POS (posBlock - 1)

	var signers []*ctypes.Validator
	s0 := &ctypes.Validator{
		Address:     coinbase.Address,
		VotingPower: 10,
	}
	signers = append(signers, s0)

	sysContract := &ctypes.SystemContracts{
		StakeManager: common.Address{},
		SlashManager: common.Address{},
		OfficialNode: common.Address{},
	}

	getCurrentValidatorsReturns := []interface{}{
		signers,
		sysContract,
		nil,
	}

	gomock.InOrder(
		mockContractClient.EXPECT().GetCurrentValidators(
			gomock.Any(),
			gomock.Any(),
		).Return(getCurrentValidatorsReturns...).Times(1),
	)

	err = testChain.Roll(t, setPoSValidatorAtBlock)
	if err != nil {
		t.Fatal(err)
	}

	// Begin POS (posBlock)

	// Roll to get the seed block
	err = testChain.Roll(t, seedBlockNumber)
	if err != nil {
		t.Fatal(err)
	}
	// seed block for next validator randomize seed
	seedBlock := testChain.Chain.GetHeaderByNumber(uint64(seedBlockNumber))

	// Note: Set calls for number 2 times for both Finalize and FinalizeAndAssemble
	// Mock the EthAPI call
	mockEthAPI.EXPECT().GetHeaderTypeByNumber(gomock.Any(), gomock.Any()).Return(seedBlock, nil).Times(2)

	// Mock the ContractClient calls
	mockContractClient.EXPECT().GetEligibleValidators(gomock.Any(), gomock.Any()).Return(signers, nil).Times(2)
	mockContractClient.EXPECT().CommitSpan(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil).Times(2)

	// commit span block
	commitSpanBlock := int(posBlock + (spanSize/2 + 1))
	err = testChain.Roll(t, commitSpanBlock)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCommitSpan_NoEligibleValidator(t *testing.T) {

	t.Helper()

	var (
		setPoSValidatorAtBlock = 49
		accountRegistry        = test.NewAccountRegistry() // Account Registry is used for storing account keys
	)

	// create coinbase account from string label
	accountRegistry.Add("coinbase")
	// get coinbase testing account
	coinbase := accountRegistry.Get("coinbase")

	// new mock controller
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// mock up all instances
	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockEthAPI := mock.NewMockEthAPI(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)

	db := rawdb.NewMemoryDatabase()

	// Load genesis from json file
	genspec := test.NewDefaultGenesis()

	// Set coinbase address
	genspec.ExtraData = make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(genspec.ExtraData[extraVanity:], coinbase.Address[:])

	// Genspec must be commited to the genesis block
	genspec.MustCommit(db)

	// Sign function, use for signing block
	signFn := func(account accounts.Account, s string, data []byte) ([]byte, error) {
		return crypto.Sign(crypto.Keccak256(data), coinbase.Key)
	}

	// Iinitialize Clique
	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	c := New(genspec.Config, db, mockEthAPI, mockContractClient)

	// Mock the inject method before authorize
	mockContractClient.EXPECT().Inject(
		gomock.Any(),
		gomock.Any(),
	).Times(1)
	c.Authorize(coinbase.Address, signFn, nil)

	// Create test chain
	testChain, err := test.NewTestChain(genspec.Config, c, db, signFn, coinbase)
	if err != nil {
		t.Fatal(err)
	}

	// Roll to number posBlock - 2
	err = testChain.Roll(t, setPoSValidatorAtBlock-1)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare POS (posBlock - 1)

	var signers []*ctypes.Validator
	s0 := &ctypes.Validator{
		// this should be empty array
	}
	signers = append(signers, s0)

	sysContract := &ctypes.SystemContracts{
		StakeManager: common.Address{},
		SlashManager: common.Address{},
		OfficialNode: common.Address{},
	}

	getCurrentValidatorsReturns := []interface{}{
		signers,
		sysContract,
		nil,
	}

	mockContractClient.EXPECT().GetCurrentValidators(
		gomock.Any(),
		gomock.Any(),
	).Return(getCurrentValidatorsReturns...).Times(1)

	err = testChain.Roll(t, setPoSValidatorAtBlock)
	if err != nil {
		t.Fatal(err)
	}

	// POS begins
	err = testChain.Next(t)
	if err == nil {
		t.Fatal(
			errors.New("provided empty elegible validator, clique should not let a block from any miners be authorized"),
		)
	}

}

func TestSlashing_Call(t *testing.T) {
	var (
		accountRegistry = test.NewAccountRegistry() // Account Registry is used for storing account keys
	)
	var posBlock uint64

	// create coinbase account from string label
	accountRegistry.Add("coinbase")
	// get coinbase testing account
	coinbase := accountRegistry.Get("coinbase")

	// This account will be slashed
	accountRegistry.Add("slashed")
	slashed := accountRegistry.Get("slashed")

	// Official node
	accountRegistry.Add("officialNode")
	officialNode := accountRegistry.Get("officialNode")

	// new mock controller
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// mock up all instances
	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockEthAPI := mock.NewMockEthAPI(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)

	db := rawdb.NewMemoryDatabase()

	// Load genesis from json file
	genspec := test.NewDefaultGenesis()

	// Set posBlock from config
	posBlock = genspec.Config.PoSBlock.Uint64()

	// Set coinbase address
	genspec.ExtraData = make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(genspec.ExtraData[extraVanity:], coinbase.Address[:])

	// Genspec must be commited to the genesis block
	genspec.MustCommit(db)

	// Sign function, use for signing block
	signFn := func(account accounts.Account, s string, data []byte) ([]byte, error) {
		return crypto.Sign(crypto.Keccak256(data), coinbase.Key)
	}

	// Iinitialize Clique
	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	c := New(genspec.Config, db, mockEthAPI, mockContractClient)

	// Mock the inject method before authorize
	mockContractClient.EXPECT().Inject(
		gomock.Any(),
		gomock.Any(),
	).Times(1)
	c.Authorize(coinbase.Address, signFn, nil)

	testChain, err := test.NewTestChain(genspec.Config, c, db, signFn, coinbase)
	if err != nil {
		t.Fatal(err)
	}

	// Roll to number posBlock - 2
	err = testChain.Roll(t, int(posBlock-2))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare value for GetCurrentValidators returns
	var signers []*ctypes.Validator
	s0 := &ctypes.Validator{
		Address:     slashed.Address,
		VotingPower: 10,
	}
	signers = append(signers, s0)

	sysContract := &ctypes.SystemContracts{
		StakeManager: common.Address{},
		SlashManager: common.Address{},
		OfficialNode: officialNode.Address,
	}

	getCurrentValidatorsReturns := []interface{}{
		signers,
		sysContract,
		nil,
	}

	mockContractClient.EXPECT().GetCurrentValidators(
		gomock.Any(),
		gomock.Any(),
	).Return(getCurrentValidatorsReturns...).Times(1)

	err = testChain.Roll(t, int(posBlock-1))
	if err != nil {
		t.Fatal(err)
	}

	statedb, err := testChain.Chain.StateAt(
		testChain.Chain.CurrentHeader().Root,
	)
	if err != nil {
		t.Fatal(err)
	}

	testHeader := &types.Header{
		Difficulty: diffNoTurn,
		Coinbase:   officialNode.Address,
		Number:     new(big.Int).SetUint64(posBlock),
		ParentHash: testChain.Chain.CurrentHeader().Hash(),
	}

	mockContractClient.EXPECT().GetCurrentSpan(
		gomock.Any(),
		gomock.Any(),
	).Return(common.Big0, nil).Times(1)

	mockContractClient.EXPECT().IsSlashed(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(false, nil).Times(1)

	mockContractClient.EXPECT().Slash(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil).Times(1)

	// Try FinalizeAndAssemble (No need to roll the blockchain)
	_, _, err = c.FinalizeAndAssemble(
		testChain.Chain,
		testHeader,
		statedb,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSlashing_ShouldNotBeCalledWhenSlashed(t *testing.T) {
	var (
		accountRegistry = test.NewAccountRegistry() // Account Registry is used for storing account keys
	)
	var posBlock uint64

	// create coinbase account from string label
	accountRegistry.Add("coinbase")
	// get coinbase testing account
	coinbase := accountRegistry.Get("coinbase")

	// This account will be slashed
	accountRegistry.Add("slashed")
	slashed := accountRegistry.Get("slashed")

	// Official node
	accountRegistry.Add("officialNode")
	officialNode := accountRegistry.Get("officialNode")

	// new mock controller
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// mock up all instances
	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockEthAPI := mock.NewMockEthAPI(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)

	db := rawdb.NewMemoryDatabase()

	// Load genesis from json file
	genspec := test.NewDefaultGenesis()

	// Set posBlock from config
	posBlock = genspec.Config.PoSBlock.Uint64()

	// Set coinbase address
	genspec.ExtraData = make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(genspec.ExtraData[extraVanity:], coinbase.Address[:])

	// Genspec must be commited to the genesis block
	genspec.MustCommit(db)

	// Sign function, use for signing block
	signFn := func(account accounts.Account, s string, data []byte) ([]byte, error) {
		return crypto.Sign(crypto.Keccak256(data), coinbase.Key)
	}

	// Iinitialize Clique
	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	c := New(genspec.Config, db, mockEthAPI, mockContractClient)

	// Mock the inject method before authorize
	mockContractClient.EXPECT().Inject(
		gomock.Any(),
		gomock.Any(),
	).Times(1)
	c.Authorize(coinbase.Address, signFn, nil)

	testChain, err := test.NewTestChain(genspec.Config, c, db, signFn, coinbase)
	if err != nil {
		t.Fatal(err)
	}

	// Roll to number posBlock - 2
	err = testChain.Roll(t, int(posBlock-2))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare value for GetCurrentValidators returns
	var signers []*ctypes.Validator
	s0 := &ctypes.Validator{
		Address:     slashed.Address,
		VotingPower: 10,
	}
	signers = append(signers, s0)

	sysContract := &ctypes.SystemContracts{
		StakeManager: common.Address{},
		SlashManager: common.Address{},
		OfficialNode: officialNode.Address,
	}

	getCurrentValidatorsReturns := []interface{}{
		signers,
		sysContract,
		nil,
	}

	mockContractClient.EXPECT().GetCurrentValidators(
		gomock.Any(),
		gomock.Any(),
	).Return(getCurrentValidatorsReturns...).Times(1)

	err = testChain.Roll(t, int(posBlock-1))
	if err != nil {
		t.Fatal(err)
	}

	statedb, err := testChain.Chain.StateAt(
		testChain.Chain.CurrentHeader().Root,
	)
	if err != nil {
		t.Fatal(err)
	}

	testHeader := &types.Header{
		Difficulty: diffNoTurn,
		Coinbase:   officialNode.Address,
		Number:     new(big.Int).SetUint64(posBlock),
		ParentHash: testChain.Chain.CurrentHeader().Hash(),
	}

	mockContractClient.EXPECT().GetCurrentSpan(
		gomock.Any(),
		gomock.Any(),
	).Return(common.Big0, nil).Times(1)

	mockContractClient.EXPECT().IsSlashed(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(true, nil).Times(1)

	mockContractClient.EXPECT().Slash(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil).Times(0)

	// Try FinalizeAndAssemble (No need to roll the blockchain)
	_, _, err = c.FinalizeAndAssemble(
		testChain.Chain,
		testHeader,
		statedb,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDistributeReward(t *testing.T) {

	var (
		bkcValidatorSet = common.HexToAddress("0x0000000000000000000000000000000000001000")
		stakeManager    = common.HexToAddress("0x0000000000000000000000000000000000002000")
		slashManager    = common.HexToAddress("0x0000000000000000000000000000000000003000")
		key, _          = crypto.HexToECDSA("b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f291")
		addr            = crypto.PubkeyToAddress(key.PublicKey)
		signer          = new(types.HomesteadSigner)
	)

	// Create the genesis block with the initial set of signers
	genesis := &core.Genesis{
		ExtraData: make([]byte, extraVanity+common.AddressLength+extraSeal),
		BaseFee:   big.NewInt(params.InitialBaseFee),
		Alloc: map[common.Address]core.GenesisAccount{
			addr: {Balance: big.NewInt(10000000000000000)},
		},
	}
	copy(genesis.ExtraData[extraVanity:], addr[:])
	// Create a pristine blockchain with the genesis injected
	db := rawdb.NewMemoryDatabase()
	genesis.Commit(db)

	// Assemble a chain of headers from the cast votes
	config := *params.TestChainConfig
	config.ErawanBlock = common.Big0
	config.PoSBlock = big.NewInt(50)
	config.MuirGlacierBlock = nil
	config.BerlinBlock = nil
	config.LondonBlock = nil
	config.ArrowGlacierBlock = nil
	config.MergeForkBlock = nil
	config.Clique = &params.CliqueConfig{
		Period:            1,
		Span:              50,
		Epoch:             300,
		ValidatorContract: bkcValidatorSet,
	}
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	mockContractClient := mock.NewMockContractClient(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	engine := New(&config, db, nil, mockContractClient)
	engine.fakeDiff = true

	chain, _ := core.NewBlockChain(db, nil, &config, engine, vm.Config{}, nil, nil)

	valz_1 := make([]ctypes.Validator, config.Clique.Span)
	for v := 0; v < int(config.Clique.Span); v++ {
		valz_1[v] = ctypes.Validator{
			Address:     addr,
			VotingPower: 100,
		}
	}

	blocks, _ := core.GenerateChain(&config, genesis.ToBlock(db), engine, db, int(config.Clique.Span)-1, func(i int, block *core.BlockGen) {
	})

	for j, block := range blocks {
		// Get the header and prepare it for signing
		header := block.Header()
		if j > 0 {
			header.ParentHash = blocks[j-1].Hash()
		}

		// Ensure the extra data has all its components
		if len(header.Extra) < extraVanity {
			header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, extraVanity-len(header.Extra))...)
		}
		header.Extra = header.Extra[:extraVanity]

		if (header.Number.Uint64()+1)%config.Clique.Span == 0 {
			for _, validator := range valz_1 {
				header.Extra = append(header.Extra, validator.HeaderBytes()...)
			}
			header.Extra = append(header.Extra, stakeManager.Bytes()...)
			header.Extra = append(header.Extra, slashManager.Bytes()...)
			header.Extra = append(header.Extra, common.Address{}.Bytes()...)
		}
		header.Extra = append(header.Extra, make([]byte, extraSeal)...)
		header.Difficulty = diffInTurn

		sig, _ := crypto.Sign(SealHash(header).Bytes(), key)
		copy(header.Extra[len(header.Extra)-extraSeal:], sig)
		blocks[j] = block.WithSeal(header)
	}

	if _, err := chain.InsertChain(blocks); err != nil {
		t.Fatalf("failed to insert initial blocks: %v", err)
	}
	if head := chain.CurrentBlock().NumberU64(); head != 49 {
		t.Fatalf("chain head mismatch: have %d, want %d", head, 49)
	}

	parent := chain.GetBlockByHash(chain.CurrentBlock().Hash())

	engine.snapshot(chain, parent.Number().Uint64(), parent.Hash(), nil)

	mockContractClient.EXPECT().DistributeToValidator(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil).Times(1)

	core.GenerateChain(&config, parent, engine, db, 1, func(i int, block *core.BlockGen) {
		tx, err := types.SignTx(types.NewTransaction(block.TxNonce(addr), common.Address{0x00}, new(big.Int), params.TxGas, genesis.BaseFee, nil), signer, key)
		if err != nil {
			panic(err)
		}
		block.AddTxWithChain(chain, tx)
	})

	// distibute 0 reward

	mockContractClient.EXPECT().DistributeToValidator(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil).Times(0)

	core.GenerateChain(&config, parent, engine, db, 1, func(i int, block *core.BlockGen) {
		tx, err := types.SignTx(types.NewTransaction(block.TxNonce(addr), common.Address{0x00}, new(big.Int), params.TxGas, big.NewInt(0), nil), signer, key)
		if err != nil {
			panic(err)
		}
		block.AddTxWithChain(chain, tx)
	})
}

func TestRandomValidator(t *testing.T) {

	t.Helper()

	var (
		setPoSValidatorAtBlock = 49
		seedBlockNumber        = 44
		accountRegistry        = test.NewAccountRegistry() // Account Registry is used for storing account keys
	)

	// create coinbase account from string label
	accountRegistry.Add("coinbase")
	// get coinbase testing account
	coinbase := accountRegistry.Get("coinbase")

	// new mock controller
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	// mock up all instances
	mockContractClient := mock.NewMockContractClient(mockCtl)
	mockEthAPI := mock.NewMockEthAPI(mockCtl)

	mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)

	db := rawdb.NewMemoryDatabase()

	// Load genesis from json file
	genspec := test.NewDefaultGenesis()

	// Set coinbase address
	genspec.ExtraData = make([]byte, extraVanity+common.AddressLength+extraSeal)
	copy(genspec.ExtraData[extraVanity:], coinbase.Address[:])

	// Genspec must be commited to the genesis block
	genspec.MustCommit(db)

	// Sign function, use for signing block
	signFn := func(account accounts.Account, s string, data []byte) ([]byte, error) {
		return crypto.Sign(crypto.Keccak256(data), coinbase.Key)
	}

	// Iinitialize Clique
	mockContractClient.EXPECT().SetSigner(gomock.Any()).AnyTimes()
	c := New(genspec.Config, db, mockEthAPI, mockContractClient)

	// Mock the inject method before authorize
	mockContractClient.EXPECT().Inject(
		gomock.Any(),
		gomock.Any(),
	).Times(1)
	c.Authorize(coinbase.Address, signFn, nil)

	// Create test chain
	testChain, err := test.NewTestChain(genspec.Config, c, db, signFn, coinbase)
	if err != nil {
		t.Fatal(err)
	}

	// Roll to number posBlock - 2
	err = testChain.Roll(t, setPoSValidatorAtBlock-1)
	if err != nil {
		t.Fatal(err)
	}

	// Prepare POS (posBlock - 1)

	signers := []*ctypes.Validator{
		{
			Address:     common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"),
			VotingPower: 30,
		},
		{
			Address:     common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"),
			VotingPower: 20,
		},
		{
			Address:     common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"),
			VotingPower: 50,
		},
	}

	sysContract := &ctypes.SystemContracts{
		StakeManager: common.Address{},
		SlashManager: common.Address{},
		OfficialNode: common.Address{},
	}

	getCurrentValidatorsReturns := []interface{}{
		signers,
		sysContract,
		nil,
	}

	mockContractClient.EXPECT().GetCurrentValidators(
		gomock.Any(),
		gomock.Any(),
	).Return(getCurrentValidatorsReturns...).Times(1)

	err = testChain.Roll(t, setPoSValidatorAtBlock)
	if err != nil {
		t.Fatal(err)
	}

	header := types.Header{}

	header.ParentHash = common.HexToHash("0x715b9a1539844f85889e8bf8ef5c570c4cef0111863b5bf3dde16ae004b544d1")
	header.Number = big.NewInt(int64(seedBlockNumber))

	// Mock the ContractClient calls
	mockContractClient.EXPECT().GetEligibleValidators(gomock.Any(), gomock.Any()).Return(signers, nil).Times(1)

	want := []*ctypes.Validator{
		{common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0xB55B31B204Cdf1Ca281B571C2dC131682A052B89"), 50}, {common.HexToAddress("0x7709a41Cae3e1b7Ac83815E6A216A4c40B25Ed0A"), 20}, {common.HexToAddress("0xD79663c4EF106dF66c138C9b93edb449BEea4032"), 30}}

	have, _ := c.selectNextValidatorSet(&header, &header)

	failed := false

	for i := 0; i < len(have); i++ {
		if have[i].Address != want[i].Address {
			failed = true
			break
		}

	}

	if failed {
		t.Error("Validators do not match")
	}

}
