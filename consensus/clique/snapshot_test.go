// Copyright 2017 The go-ethereum Authors
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
	"crypto/ecdsa"
	"math/big"
	"sort"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
	"github.com/ethereum/go-ethereum/consensus/clique/mock"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/golang/mock/gomock"
)

// testerAccountPool is a pool to maintain currently active tester accounts,
// mapped from textual names used in the tests below to actual Ethereum private
// keys capable of signing transactions.
type testerAccountPool struct {
	accounts map[string]*ecdsa.PrivateKey
}

func newTesterAccountPool() *testerAccountPool {
	return &testerAccountPool{
		accounts: make(map[string]*ecdsa.PrivateKey),
	}
}

// checkpoint creates a Clique checkpoint signer section from the provided list
// of authorized signers and embeds it into the provided header.
func (ap *testerAccountPool) checkpoint(header *types.Header, signers []string) {
	auths := make([]common.Address, len(signers))
	for i, signer := range signers {
		auths[i] = ap.address(signer)
	}
	sort.Sort(signersAscending(auths))
	for i, auth := range auths {
		copy(header.Extra[extraVanity+i*common.AddressLength:], auth.Bytes())
	}
}

// address retrieves the Ethereum address of a tester account by label, creating
// a new account if no previous one exists yet.
func (ap *testerAccountPool) address(account string) common.Address {
	// Return the zero account for non-addresses
	if account == "" {
		return common.Address{}
	}
	// Ensure we have a persistent key for the account
	if ap.accounts[account] == nil {
		ap.accounts[account], _ = crypto.GenerateKey()
	}
	// Resolve and return the Ethereum address
	return crypto.PubkeyToAddress(ap.accounts[account].PublicKey)
}

// sign calculates a Clique digital signature for the given block and embeds it
// back into the header.
func (ap *testerAccountPool) sign(header *types.Header, signer string) {
	// Ensure we have a persistent key for the signer
	if ap.accounts[signer] == nil {
		ap.accounts[signer], _ = crypto.GenerateKey()
	}
	// Sign the header and embed the signature in extra data
	sig, _ := crypto.Sign(SealHash(header).Bytes(), ap.accounts[signer])
	copy(header.Extra[len(header.Extra)-extraSeal:], sig)
}

// testerVote represents a single block signed by a parcitular account, where
// the account may or may not have cast a Clique vote.
type testerVote struct {
	signer     string
	voted      string
	auth       bool
	checkpoint []string
	newbatch   bool
}

// Tests that Clique signer voting is evaluated correctly for various simple and
// complex scenarios, as well as that a few special corner cases fail correctly.
func TestClique(t *testing.T) {
	// Define the various voting scenarios to test
	tests := []struct {
		epoch   uint64
		signers []string
		votes   []testerVote
		results []string
		failure error
	}{
		{
			// Single signer, no votes cast
			signers: []string{"A"},
			votes:   []testerVote{{signer: "A"}},
			results: []string{"A"},
		}, {
			// Single signer, voting to add two others (only accept first, second needs 2 votes)
			signers: []string{"A"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: true},
				{signer: "B"},
				{signer: "A", voted: "C", auth: true},
			},
			results: []string{"A", "B"},
		}, {
			// Two signers, voting to add three others (only accept first two, third needs 3 votes already)
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: true},
				{signer: "B", voted: "C", auth: true},
				{signer: "A", voted: "D", auth: true},
				{signer: "B", voted: "D", auth: true},
				{signer: "C"},
				{signer: "A", voted: "E", auth: true},
				{signer: "B", voted: "E", auth: true},
			},
			results: []string{"A", "B", "C", "D"},
		}, {
			// Single signer, dropping itself (weird, but one less cornercase by explicitly allowing this)
			signers: []string{"A"},
			votes: []testerVote{
				{signer: "A", voted: "A", auth: false},
			},
			results: []string{},
		}, {
			// Two signers, actually needing mutual consent to drop either of them (not fulfilled)
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: false},
			},
			results: []string{"A", "B"},
		}, {
			// Two signers, actually needing mutual consent to drop either of them (fulfilled)
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: false},
				{signer: "B", voted: "B", auth: false},
			},
			results: []string{"A"},
		}, {
			// Three signers, two of them deciding to drop the third
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B", voted: "C", auth: false},
			},
			results: []string{"A", "B"},
		}, {
			// Four signers, consensus of two not being enough to drop anyone
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B", voted: "C", auth: false},
			},
			results: []string{"A", "B", "C", "D"},
		}, {
			// Four signers, consensus of three already being enough to drop someone
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "D", auth: false},
				{signer: "B", voted: "D", auth: false},
				{signer: "C", voted: "D", auth: false},
			},
			results: []string{"A", "B", "C"},
		}, {
			// Authorizations are counted once per signer per target
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: true},
				{signer: "B"},
				{signer: "A", voted: "C", auth: true},
				{signer: "B"},
				{signer: "A", voted: "C", auth: true},
			},
			results: []string{"A", "B"},
		}, {
			// Authorizing multiple accounts concurrently is permitted
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: true},
				{signer: "B"},
				{signer: "A", voted: "D", auth: true},
				{signer: "B"},
				{signer: "A"},
				{signer: "B", voted: "D", auth: true},
				{signer: "A"},
				{signer: "B", voted: "C", auth: true},
			},
			results: []string{"A", "B", "C", "D"},
		}, {
			// Deauthorizations are counted once per signer per target
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: false},
				{signer: "B"},
				{signer: "A", voted: "B", auth: false},
				{signer: "B"},
				{signer: "A", voted: "B", auth: false},
			},
			results: []string{"A", "B"},
		}, {
			// Deauthorizing multiple accounts concurrently is permitted
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B"},
				{signer: "C"},
				{signer: "A", voted: "D", auth: false},
				{signer: "B"},
				{signer: "C"},
				{signer: "A"},
				{signer: "B", voted: "D", auth: false},
				{signer: "C", voted: "D", auth: false},
				{signer: "A"},
				{signer: "B", voted: "C", auth: false},
			},
			results: []string{"A", "B"},
		}, {
			// Votes from deauthorized signers are discarded immediately (deauth votes)
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "C", voted: "B", auth: false},
				{signer: "A", voted: "C", auth: false},
				{signer: "B", voted: "C", auth: false},
				{signer: "A", voted: "B", auth: false},
			},
			results: []string{"A", "B"},
		}, {
			// Votes from deauthorized signers are discarded immediately (auth votes)
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "C", voted: "D", auth: true},
				{signer: "A", voted: "C", auth: false},
				{signer: "B", voted: "C", auth: false},
				{signer: "A", voted: "D", auth: true},
			},
			results: []string{"A", "B"},
		}, {
			// Cascading changes are not allowed, only the account being voted on may change
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B"},
				{signer: "C"},
				{signer: "A", voted: "D", auth: false},
				{signer: "B", voted: "C", auth: false},
				{signer: "C"},
				{signer: "A"},
				{signer: "B", voted: "D", auth: false},
				{signer: "C", voted: "D", auth: false},
			},
			results: []string{"A", "B", "C"},
		}, {
			// Changes reaching consensus out of bounds (via a deauth) execute on touch
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B"},
				{signer: "C"},
				{signer: "A", voted: "D", auth: false},
				{signer: "B", voted: "C", auth: false},
				{signer: "C"},
				{signer: "A"},
				{signer: "B", voted: "D", auth: false},
				{signer: "C", voted: "D", auth: false},
				{signer: "A"},
				{signer: "C", voted: "C", auth: true},
			},
			results: []string{"A", "B"},
		}, {
			// Changes reaching consensus out of bounds (via a deauth) may go out of consensus on first touch
			signers: []string{"A", "B", "C", "D"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B"},
				{signer: "C"},
				{signer: "A", voted: "D", auth: false},
				{signer: "B", voted: "C", auth: false},
				{signer: "C"},
				{signer: "A"},
				{signer: "B", voted: "D", auth: false},
				{signer: "C", voted: "D", auth: false},
				{signer: "A"},
				{signer: "B", voted: "C", auth: true},
			},
			results: []string{"A", "B", "C"},
		}, {
			// Ensure that pending votes don't survive authorization status changes. This
			// corner case can only appear if a signer is quickly added, removed and then
			// readded (or the inverse), while one of the original voters dropped. If a
			// past vote is left cached in the system somewhere, this will interfere with
			// the final signer outcome.
			signers: []string{"A", "B", "C", "D", "E"},
			votes: []testerVote{
				{signer: "A", voted: "F", auth: true}, // Authorize F, 3 votes needed
				{signer: "B", voted: "F", auth: true},
				{signer: "C", voted: "F", auth: true},
				{signer: "D", voted: "F", auth: false}, // Deauthorize F, 4 votes needed (leave A's previous vote "unchanged")
				{signer: "E", voted: "F", auth: false},
				{signer: "B", voted: "F", auth: false},
				{signer: "C", voted: "F", auth: false},
				{signer: "D", voted: "F", auth: true}, // Almost authorize F, 2/3 votes needed
				{signer: "E", voted: "F", auth: true},
				{signer: "B", voted: "A", auth: false}, // Deauthorize A, 3 votes needed
				{signer: "C", voted: "A", auth: false},
				{signer: "D", voted: "A", auth: false},
				{signer: "B", voted: "F", auth: true}, // Finish authorizing F, 3/3 votes needed
			},
			results: []string{"B", "C", "D", "E", "F"},
		}, {
			// Epoch transitions reset all votes to allow chain checkpointing
			epoch:   3,
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: true},
				{signer: "B"},
				{signer: "A", checkpoint: []string{"A", "B"}},
				{signer: "B", voted: "C", auth: true},
			},
			results: []string{"A", "B"},
		}, {
			// An unauthorized signer should not be able to sign blocks
			signers: []string{"A"},
			votes: []testerVote{
				{signer: "B"},
			},
			failure: errUnauthorizedSigner,
		}, {
			// An authorized signer that signed recenty should not be able to sign again
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A"},
				{signer: "A"},
			},
			failure: errRecentlySigned,
		}, {
			// Recent signatures should not reset on checkpoint blocks imported in a batch
			epoch:   3,
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "A"},
				{signer: "B"},
				{signer: "A", checkpoint: []string{"A", "B", "C"}},
				{signer: "A"},
			},
			failure: errRecentlySigned,
		}, {
			// Recent signatures should not reset on checkpoint blocks imported in a new
			// batch (https://github.com/ethereum/go-ethereum/issues/17593). Whilst this
			// seems overly specific and weird, it was a Rinkeby consensus split.
			epoch:   3,
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "A"},
				{signer: "B"},
				{signer: "A", checkpoint: []string{"A", "B", "C"}},
				{signer: "A", newbatch: true},
			},
			failure: errRecentlySigned,
		},
	}
	// Run through the scenarios and test them
	for i, tt := range tests {
		// Create the account pool and generate the initial set of signers
		accounts := newTesterAccountPool()

		signers := make([]common.Address, len(tt.signers))
		for j, signer := range tt.signers {
			signers[j] = accounts.address(signer)
		}
		for j := 0; j < len(signers); j++ {
			for k := j + 1; k < len(signers); k++ {
				if bytes.Compare(signers[j][:], signers[k][:]) > 0 {
					signers[j], signers[k] = signers[k], signers[j]
				}
			}
		}
		// Create the genesis block with the initial set of signers
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+common.AddressLength*len(signers)+extraSeal),
			BaseFee:   big.NewInt(params.InitialBaseFee),
		}
		for j, signer := range signers {
			copy(genesis.ExtraData[extraVanity+j*common.AddressLength:], signer[:])
		}
		// Create a pristine blockchain with the genesis injected
		db := rawdb.NewMemoryDatabase()
		genesis.Commit(db)

		// Assemble a chain of headers from the cast votes
		config := *params.TestChainConfig
		config.Clique = &params.CliqueConfig{
			Period: 1,
			Epoch:  tt.epoch,
		}
		mockCtl := gomock.NewController(t)
		defer mockCtl.Finish()

		mockContractClient := mock.NewMockContractClient(mockCtl)
		mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)
		engine := New(&config, db, nil, mockContractClient)

		engine.fakeDiff = true

		blocks, _ := core.GenerateChain(&config, genesis.ToBlock(db), engine, db, len(tt.votes), func(j int, gen *core.BlockGen) {
			// Cast the vote contained in this block
			if config.IsErawan(gen.Number()) {
				gen.SetMixDigest(accounts.address(tt.votes[j].voted))
			} else {
				gen.SetCoinbase(accounts.address(tt.votes[j].voted))
			}
			if tt.votes[j].auth {
				var nonce types.BlockNonce
				copy(nonce[:], nonceAuthVote)
				gen.SetNonce(nonce)
			}
		})
		// Iterate through the blocks and seal them individually
		for j, block := range blocks {
			// Get the header and prepare it for signing
			header := block.Header()
			if j > 0 {
				header.ParentHash = blocks[j-1].Hash()
			}
			header.Extra = make([]byte, extraVanity+extraSeal)
			if auths := tt.votes[j].checkpoint; auths != nil {
				header.Extra = make([]byte, extraVanity+len(auths)*common.AddressLength+extraSeal)
				accounts.checkpoint(header, auths)
			}
			header.Difficulty = diffInTurn // Ignored, we just need a valid number

			// Generate the signature, embed it into the header and the block
			accounts.sign(header, tt.votes[j].signer)
			blocks[j] = block.WithSeal(header)
		}
		// Split the blocks up into individual import batches (cornercase testing)
		batches := [][]*types.Block{nil}
		for j, block := range blocks {
			if tt.votes[j].newbatch {
				batches = append(batches, nil)
			}
			batches[len(batches)-1] = append(batches[len(batches)-1], block)
		}
		// Pass all the headers through clique and ensure tallying succeeds
		chain, err := core.NewBlockChain(db, nil, &config, engine, vm.Config{}, nil, nil)
		if err != nil {
			t.Errorf("test %d: failed to create test chain: %v", i, err)
			continue
		}
		failed := false
		for j := 0; j < len(batches)-1; j++ {
			if k, err := chain.InsertChain(batches[j]); err != nil {
				t.Errorf("test %d: failed to import batch %d, block %d: %v", i, j, k, err)
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		if _, err = chain.InsertChain(batches[len(batches)-1]); err != tt.failure {
			t.Errorf("test %d: failure mismatch: have %v, want %v", i, err, tt.failure)
		}
		if tt.failure != nil {
			continue
		}
		// No failure was produced or requested, generate the final voting snapshot
		head := blocks[len(blocks)-1]

		snap, err := engine.snapshot(chain, head.NumberU64(), head.Hash(), nil)
		if err != nil {
			t.Errorf("test %d: failed to retrieve voting snapshot: %v", i, err)
			continue
		}
		// Verify the final list of signers against the expected ones
		signers = make([]common.Address, len(tt.results))
		for j, signer := range tt.results {
			signers[j] = accounts.address(signer)
		}
		for j := 0; j < len(signers); j++ {
			for k := j + 1; k < len(signers); k++ {
				if bytes.Compare(signers[j][:], signers[k][:]) > 0 {
					signers[j], signers[k] = signers[k], signers[j]
				}
			}
		}
		result := snap.signers()
		if len(result) != len(signers) {
			t.Errorf("test %d: signers mismatch: have %x, want %x", i, result, signers)
			continue
		}
		for j := 0; j < len(result); j++ {
			if !bytes.Equal(result[j][:], signers[j][:]) {
				t.Errorf("test %d, signer %d: signer mismatch: have %x, want %x", i, j, result[j], signers[j])
			}
		}
	}
}

func TestCliqueErawanTransition(t *testing.T) {
	// Define the various voting scenarios to test
	tests := []struct {
		epoch       uint64
		signers     []string
		votes       []testerVote
		results     []string
		erawanBlock *big.Int
	}{
		{
			// Single signer, voting to add two others (only accept first, second needs 2 votes)
			signers: []string{"A"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: true},
				{signer: "B"},
				{signer: "A", voted: "C", auth: true},
			},
			results:     []string{"A", "B"},
			erawanBlock: big.NewInt(2),
		}, {
			// Two signers, voting to add three others (only accept first two, third needs 3 votes already)
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: true},
				{signer: "B", voted: "C", auth: true},
				{signer: "A", voted: "D", auth: true},
				{signer: "B", voted: "D", auth: true},
				{signer: "C"},
				{signer: "A", voted: "E", auth: true},
				{signer: "B", voted: "E", auth: true},
			},
			results:     []string{"A", "B", "C", "D"},
			erawanBlock: big.NewInt(4),
		}, {
			// Two signers, actually needing mutual consent to drop either of them (fulfilled)
			signers: []string{"A", "B"},
			votes: []testerVote{
				{signer: "A", voted: "B", auth: false},
				{signer: "B", voted: "B", auth: false},
			},
			results:     []string{"A"},
			erawanBlock: big.NewInt(2),
		}, {
			// Three signers, two of them deciding to drop the third
			signers: []string{"A", "B", "C"},
			votes: []testerVote{
				{signer: "A", voted: "C", auth: false},
				{signer: "B", voted: "C", auth: false},
			},
			results:     []string{"A", "B"},
			erawanBlock: big.NewInt(2),
		},
	}
	// Run through the scenarios and test them
	for i, tt := range tests {
		// Create the account pool and generate the initial set of signers
		accounts := newTesterAccountPool()

		signers := make([]common.Address, len(tt.signers))
		for j, signer := range tt.signers {
			signers[j] = accounts.address(signer)
		}
		for j := 0; j < len(signers); j++ {
			for k := j + 1; k < len(signers); k++ {
				if bytes.Compare(signers[j][:], signers[k][:]) > 0 {
					signers[j], signers[k] = signers[k], signers[j]
				}
			}
		}
		// Create the genesis block with the initial set of signers
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+common.AddressLength*len(signers)+extraSeal),
			BaseFee:   big.NewInt(params.InitialBaseFee),
		}
		for j, signer := range signers {
			copy(genesis.ExtraData[extraVanity+j*common.AddressLength:], signer[:])
		}
		// Create a pristine blockchain with the genesis injected
		db := rawdb.NewMemoryDatabase()
		genesis.Commit(db)

		// Assemble a chain of headers from the cast votes
		config := *params.TestChainConfig
		config.ErawanBlock = tt.erawanBlock
		config.MuirGlacierBlock = nil
		config.BerlinBlock = nil
		config.LondonBlock = nil
		config.ArrowGlacierBlock = nil
		config.MergeForkBlock = nil
		config.Clique = &params.CliqueConfig{
			Period: 1,
			Epoch:  tt.epoch,
		}

		mockCtl := gomock.NewController(t)
		defer mockCtl.Finish()

		mockContractClient := mock.NewMockContractClient(mockCtl)
		mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)
		engine := New(&config, db, nil, mockContractClient)
		engine.fakeDiff = true

		blocks, _ := core.GenerateChain(&config, genesis.ToBlock(db), engine, db, len(tt.votes), func(j int, gen *core.BlockGen) {
			// Cast the vote contained in this block
			if config.IsErawan(gen.Number()) {
				gen.SetMixDigest(accounts.address(tt.votes[j].voted))
			} else {
				gen.SetCoinbase(accounts.address(tt.votes[j].voted))
			}
			if tt.votes[j].auth {
				var nonce types.BlockNonce
				copy(nonce[:], nonceAuthVote)
				gen.SetNonce(nonce)
			}
		})
		// Iterate through the blocks and seal them individually
		for j, block := range blocks {
			// Get the header and prepare it for signing
			header := block.Header()
			if j > 0 {
				header.ParentHash = blocks[j-1].Hash()
			}
			header.Extra = make([]byte, extraVanity+extraSeal)
			if auths := tt.votes[j].checkpoint; auths != nil {
				header.Extra = make([]byte, extraVanity+len(auths)*common.AddressLength+extraSeal)
				accounts.checkpoint(header, auths)
			}
			header.Difficulty = diffInTurn // Ignored, we just need a valid number

			// Generate the signature, embed it into the header and the block
			accounts.sign(header, tt.votes[j].signer)
			blocks[j] = block.WithSeal(header)
		}
		// Split the blocks up into individual import batches (cornercase testing)
		batches := [][]*types.Block{nil}
		for j, block := range blocks {
			if tt.votes[j].newbatch {
				batches = append(batches, nil)
			}
			batches[len(batches)-1] = append(batches[len(batches)-1], block)
		}
		// Pass all the headers through clique and ensure tallying succeeds
		chain, err := core.NewBlockChain(db, nil, &config, engine, vm.Config{}, nil, nil)
		if err != nil {
			t.Errorf("test %d: failed to create test chain: %v", i, err)
			continue
		}
		failed := false
		for j := 0; j < len(batches)-1; j++ {
			if k, err := chain.InsertChain(batches[j]); err != nil {
				t.Errorf("test %d: failed to import batch %d, block %d: %v", i, j, k, err)
				failed = true
				break
			}
		}
		if failed {
			continue
		}
		_, err = chain.InsertChain(batches[len(batches)-1])
		if err != nil {
			t.Errorf("test %d failed: %v", i, err)
		}
		// No failure was produced or requested, generate the final voting snapshot
		head := blocks[len(blocks)-1]

		snap, err := engine.snapshot(chain, head.NumberU64(), head.Hash(), nil)
		if err != nil {
			t.Errorf("test %d: failed to retrieve voting snapshot: %v", i, err)
			continue
		}
		// Verify the final list of signers against the expected ones
		signers = make([]common.Address, len(tt.results))
		for j, signer := range tt.results {
			signers[j] = accounts.address(signer)
		}
		for j := 0; j < len(signers); j++ {
			for k := j + 1; k < len(signers); k++ {
				if bytes.Compare(signers[j][:], signers[k][:]) > 0 {
					signers[j], signers[k] = signers[k], signers[j]
				}
			}
		}
		result := snap.signers()
		if len(result) != len(signers) {
			t.Errorf("test %d: signers mismatch: have %x, want %x", i, result, signers)
			continue
		}
		for j := 0; j < len(result); j++ {
			if !bytes.Equal(result[j][:], signers[j][:]) {
				t.Errorf("test %d, signer %d: signer mismatch: have %x, want %x", i, j, result[j], signers[j])
			}
		}
	}
}

func TestCliquePoSTransition(t *testing.T) {
	type validators struct {
		address string
		power   uint64
	}
	tests := []struct {
		firstValidatorSet []validators
		epoch             uint64
		signers           []string
		results           []string
		validators        []string
		checkValidates    []common.Address
	}{
		{
			firstValidatorSet: []validators{
				{
					address: "B",
					power:   10,
				},
				{
					address: "C",
					power:   10,
				},
			},
			signers: []string{"A", "B"},
			results: []string{"A", "B"},
		},
	}

	// Run through the scenarios and test them
	for _, tt := range tests {
		// Create the account pool and generate the initial set of signers
		accounts := newTesterAccountPool()

		signers := make([]common.Address, len(tt.signers))
		for j, signer := range tt.signers {
			signers[j] = accounts.address(signer)
		}
		for j := 0; j < len(signers); j++ {
			for k := j + 1; k < len(signers); k++ {
				if bytes.Compare(signers[j][:], signers[k][:]) > 0 {
					signers[j], signers[k] = signers[k], signers[j]
				}
			}
		}
		// Create the genesis block with the initial set of signers
		genesis := &core.Genesis{
			ExtraData: make([]byte, extraVanity+common.AddressLength*len(signers)+extraSeal),
			BaseFee:   big.NewInt(params.InitialBaseFee),
		}
		for j, signer := range signers {
			copy(genesis.ExtraData[extraVanity+j*common.AddressLength:], signer[:])
		}
		// Create a pristine blockchain with the genesis injected
		db := rawdb.NewMemoryDatabase()
		genesis.Commit(db)

		// Assemble a chain of headers from the cast votes
		config := *params.TestChainConfig
		config.ErawanBlock = common.Big0
		config.ChaophrayaBlock = big.NewInt(50)
		config.MuirGlacierBlock = nil
		config.BerlinBlock = nil
		config.LondonBlock = nil
		config.ArrowGlacierBlock = nil
		config.MergeForkBlock = nil
		config.Clique = &params.CliqueConfig{
			Period: 1,
			Span:   50,
			Epoch:  300,
		}
		mockCtl := gomock.NewController(t)
		defer mockCtl.Finish()

		mockContractClient := mock.NewMockContractClient(mockCtl)
		mockContractClient.EXPECT().SetSigner(gomock.Any()).Times(1)
		engine := New(&config, db, nil, mockContractClient)
		engine.fakeDiff = true

		valz_1 := make([]ctypes.Validator, config.Clique.Span)
		for v := 0; v < int(config.Clique.Span); v++ {
			valz_1[v] = ctypes.Validator{
				Address:     accounts.address(tt.firstValidatorSet[v%len(tt.firstValidatorSet)].address),
				VotingPower: tt.firstValidatorSet[0].power,
			}
			tt.checkValidates = append(tt.checkValidates, accounts.address(tt.firstValidatorSet[v%len(tt.firstValidatorSet)].address))
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
				header.Extra = append(header.Extra, common.Address{}.Bytes()...)
				header.Extra = append(header.Extra, common.Address{}.Bytes()...)
				header.Extra = append(header.Extra, common.Address{}.Bytes()...)
			}
			header.Extra = append(header.Extra, make([]byte, extraSeal)...)
			header.Difficulty = diffInTurn

			accounts.sign(header, tt.signers[j%len(signers)])
			blocks[j] = block.WithSeal(header)
		}

		chain, _ := core.NewBlockChain(db, nil, &config, engine, vm.Config{}, nil, nil)
		_, err := chain.InsertChain(blocks)
		if err != nil {
			panic(err)
		}

		parent := chain.GetBlockByHash(chain.CurrentBlock().Hash())

		engine.snapshot(chain, parent.Number().Uint64(), parent.Hash(), nil)

		block50, _ := core.GenerateChain(&config, parent, engine, db, 1, func(i int, block *core.BlockGen) {})

		chain, _ = core.NewBlockChain(db, nil, &config, engine, vm.Config{}, nil, nil)

		for j, block := range block50 {
			// Get the header and prepare it for signing
			header := block.Header()
			if j > 0 {
				header.ParentHash = block50[j-1].Hash()
			}

			// Ensure the extra data has all its components
			if len(header.Extra) < extraVanity {
				header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, extraVanity-len(header.Extra))...)
			}
			header.Extra = header.Extra[:extraVanity]
			header.Extra = append(header.Extra, make([]byte, extraSeal)...)
			header.Difficulty = diffInTurn

			accounts.sign(header, tt.firstValidatorSet[j%int(config.Clique.Span)].address)
			block50[j] = block.WithSeal(header)
		}

		_, err = chain.InsertChain(block50)
		if err != nil {
			panic(err)
		}

		parent = chain.GetBlockByHash(chain.CurrentBlock().Hash())

		snap, _ := engine.snapshot(chain, parent.Number().Uint64(), parent.Hash(), nil)

		for c := 0; c < len(snap.Validators); c++ {
			if bytes.Compare(tt.checkValidates[c][:], snap.Validators[c][:]) > 0 {
				t.Errorf("validators mismatch: have %x, want %x", snap.Validators[c], tt.checkValidates[c])
			}
		}
		if len(tt.results) != len(snap.Signers) {
			t.Errorf("signers mismatch: have %d, want %d", len(snap.Signers), len(snap.Signers))
			continue
		}
	}
}
