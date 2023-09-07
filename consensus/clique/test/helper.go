package test

import (
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
)

var (
	extraSeal = 65
)

func NewDefaultConfig() *params.ChainConfig {

	var (
		chaophrayaBlock uint64 = 50
	)
	c := params.AllCliqueProtocolChanges
	c.Clique.Span = 50
	c.ChaophrayaBlock = new(big.Int).SetUint64(chaophrayaBlock)
	c.BerlinBlock = nil
	c.LondonBlock = nil
	c.MuirGlacierBlock = nil
	return c
}

func NewDefaultGenesis() *core.Genesis {

	var (
		difficulty uint64 = 1
	)
	g := &core.Genesis{
		Config:     NewDefaultConfig(),
		Number:     0,
		Nonce:      0,
		GasUsed:    0,
		GasLimit:   10000000,
		Timestamp:  1617023722,
		Coinbase:   common.Address{},
		Difficulty: new(big.Int).SetUint64(difficulty),
		Mixhash:    common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
		ParentHash: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
		Alloc:      make(core.GenesisAlloc),
	}
	return g
}

func LoadGenesis(t *testing.T, filePath string) *core.Genesis {
	genesisData, err := ioutil.ReadFile(filePath)
	if err != nil {
		t.Fatalf("%s", err)
	}

	gen := &core.Genesis{}

	if err := json.Unmarshal(genesisData, gen); err != nil {
		t.Fatalf("%s", err)
	}
	return gen
}

type SignFn = func(account accounts.Account, s string, data []byte) ([]byte, error)

type Account struct {
	Address common.Address
	Key     *ecdsa.PrivateKey
}

func newAccount() Account {
	k, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(k.PublicKey)
	return Account{
		Address: addr,
		Key:     k,
	}
}

type AccountRegistry struct {
	accounts map[string]Account
}

func NewAccountRegistry() *AccountRegistry {
	a := make(map[string]Account)
	return &AccountRegistry{
		accounts: a,
	}
}

func (r *AccountRegistry) Add(label string) {
	if r.accounts[label].Address != (common.Address{}) {
		fmt.Println("Already add this account!!")
		return
	}
	account := newAccount()
	r.accounts[label] = account
}

func (r *AccountRegistry) Get(label string) Account {
	return r.accounts[label]
}

type TestChain struct {
	Chain           *core.BlockChain
	Genesis         *core.Genesis
	SignFn          SignFn
	CoinbaseAccount Account

	db ethdb.Database
}

func NewTestChain(genesis *core.Genesis, engine consensus.Engine, db ethdb.Database, signFn SignFn, coinbaseAccount Account) (*TestChain, error) {
	chain, err := core.NewBlockChain(db, nil, genesis, nil, engine, vm.Config{}, nil, nil)
	if err != nil {
		return &TestChain{}, err
	}
	tc := &TestChain{
		Chain:           chain,
		Genesis:         genesis,
		SignFn:          signFn,
		CoinbaseAccount: coinbaseAccount,
		db:              db,
	}
	return tc, nil
}

func (tc *TestChain) Roll(t *testing.T, n int) error {
	current := tc.Chain.CurrentHeader().Number.Int64()
	diff := n - int(current)
	for i := 0; i < diff; i++ {
		err := tc.mineBlock(t)
		if err != nil {
			return err
		}
	}
	return nil
}

func (tc *TestChain) SetCoinbase(a Account) {
	tc.CoinbaseAccount = a
}

func (tc *TestChain) Next(t *testing.T) error {
	return tc.mineBlock(t)
}

func (tc *TestChain) mineBlock(t *testing.T) error {
	t.Helper()

	parent := tc.Chain.CurrentHeader()

	header := &types.Header{
		Number:     new(big.Int).Add(parent.Number, common.Big1),
		ParentHash: parent.Hash(),
		GasLimit:   10000000,
	}
	// prepare work (header)
	err := tc.Chain.Engine().Prepare(tc.Chain, header)
	if err != nil {
		return err
	}

	// load statedb
	state, err := tc.Chain.StateAt(parent.Root)

	if err != nil {
		return err
	}
	// generate work (block)
	block, _, err := tc.Chain.Engine().FinalizeAndAssemble(
		tc.Chain,
		header,
		state,
		nil,
		nil,
		nil,
	)
	// ignore txs root hash verification
	header.TxHash = block.TxHash()
	header.ReceiptHash = block.ReceiptHash()

	if err != nil {
		return err
	}

	// get seal hash
	sealHash := tc.Chain.Engine().SealHash(header)

	// sign block
	sig, _ := crypto.Sign(sealHash.Bytes(), tc.CoinbaseAccount.Key)
	copy(header.Extra[len(header.Extra)-extraSeal:], sig)

	block = block.WithSeal(header)

	_, err = tc.Chain.InsertChain([]*types.Block{block})
	if err != nil {
		return err
	}
	return err

}
