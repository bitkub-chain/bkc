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

// Package clique implements the proof-of-authority consensus engine.
package clique

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/clique/ctypes"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/ethereum/go-ethereum/trie"
	lru "github.com/hashicorp/golang-lru"
	"golang.org/x/crypto/sha3"
)

const (
	checkpointInterval = 1024 // Number of blocks after which to save the vote snapshot to the database
	inmemorySnapshots  = 128  // Number of recent vote snapshots to keep in memory
	inmemorySignatures = 4096 // Number of recent block signatures to keep in memory

	validatorBytesLength = 40                     // Validator has 20 bytes for an address and 20 for a power
	contractBytesLength  = 60                     // Bytes length of 3 PoS contracts (20 each)
	totalPosContracts    = 3                      // Number of PoS contracts checked when retrieving from the validator set contract
	wiggleTime           = 500 * time.Millisecond // Random delay (per signer) to allow concurrent signers
)

// Clique proof-of-authority protocol constants.
// Also included PoS constants.
var (
	epochLength = uint64(30000) // Default number of blocks after which to checkpoint and reset the pending votes

	extraVanity = 32                     // Fixed number of extra-data prefix bytes reserved for signer vanity
	extraSeal   = crypto.SignatureLength // Fixed number of extra-data suffix bytes reserved for signer seal

	nonceAuthVote = hexutil.MustDecode("0xffffffffffffffff") // Magic nonce number to vote on adding a new signer
	nonceDropVote = hexutil.MustDecode("0x0000000000000000") // Magic nonce number to vote on removing a signer.

	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.

	diffInTurn = big.NewInt(2) // Block difficulty for in-turn signatures
	diffNoTurn = big.NewInt(1) // Block difficulty for out-of-turn signatures
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")

	// errInvalidCheckpointBeneficiary is returned if a checkpoint/epoch transition
	// block has a beneficiary set to non-zeroes.
	errInvalidCheckpointBeneficiary = errors.New("beneficiary in checkpoint block non-zero")

	// errInvalidVote is returned if a nonce value is something else that the two
	// allowed constants of 0x00..0 or 0xff..f.
	errInvalidVote = errors.New("vote nonce not 0x00..0 or 0xff..f")

	// errInvalidCheckpointVote is returned if a checkpoint/epoch transition block
	// has a vote nonce set to non-zeroes.
	errInvalidCheckpointVote = errors.New("vote nonce in checkpoint block non-zero")

	// errMissingVanity is returned if a block's extra-data section is shorter than
	// 32 bytes, which is required to store the signer vanity.
	errMissingVanity = errors.New("extra-data 32 byte vanity prefix missing")

	// errMissingSignature is returned if a block's extra-data section doesn't seem
	// to contain a 65 byte secp256k1 signature.
	errMissingSignature = errors.New("extra-data 65 byte signature suffix missing")

	// errExtraSigners is returned if non-checkpoint block contain signer data in
	// their extra-data fields.
	errExtraSigners = errors.New("non-checkpoint block contains extra signer list")

	// errInvalidCheckpointSigners is returned if a checkpoint block contains an
	// invalid list of signers (i.e. non divisible by 20 bytes).
	errInvalidCheckpointSigners = errors.New("invalid signer list on checkpoint block")

	// errMismatchingCheckpointSigners is returned if a checkpoint block contains a
	// list of signers different than the one the local node calculated.
	errMismatchingCheckpointSigners = errors.New("mismatching signer list on checkpoint block")

	// errMismatchingSpanValidators is returned if a sprint block contains a
	// list of validators different than the one the local node calculated.
	errMismatchingSpanValidators = errors.New("mismatching validator list on span block")

	// errInvalidMixDigest is returned if a block's mix digest is non-zero.
	errInvalidMixDigest = errors.New("non-zero mix digest")

	// errInvalidUncleHash is returned if a block contains an non-empty uncle list.
	errInvalidUncleHash = errors.New("non empty uncle hash")

	// errInvalidDifficulty is returned if the difficulty of a block neither 1 or 2.
	errInvalidDifficulty = errors.New("invalid difficulty")

	// errWrongDifficulty is returned if the difficulty of a block doesn't match the
	// turn of the signer.
	errWrongDifficulty = errors.New("wrong difficulty")

	// errInvalidTimestamp is returned if the timestamp of a block is lower than
	// the previous block's timestamp + the minimum block period.
	errInvalidTimestamp = errors.New("invalid timestamp")

	// errInvalidVotingChain is returned if an authorization list is attempted to
	// be modified via out-of-range or non-contiguous headers.
	errInvalidVotingChain = errors.New("invalid voting chain")

	// errUnauthorizedSigner is returned if a header is signed by a non-authorized entity.
	errUnauthorizedSigner = errors.New("unauthorized signer")

	// errRecentlySigned is returned if a header is signed by an authorized entity
	// that already signed a header recently, thus is temporarily not allowed to.
	errRecentlySigned = errors.New("recently signed")

	// Fail to get the given snapshot
	errGetSnapshotFailed = errors.New("fail to get the snapshot")

	// Invalid span
	errInvalidSpan = errors.New("invalid span")
)

func (c *Clique) isToSystemContract(to common.Address, snap *Snapshot) bool {
	// Map system contracts
	systemContracts := map[common.Address]bool{
		c.config.Clique.ValidatorContractV2: true,
		c.config.Clique.ValidatorContract:   true,
		snap.SystemContracts.StakeManager:   true,
		snap.SystemContracts.SlashManager:   true,
	}
	return systemContracts[to]
}

// ecrecover extracts the Ethereum account address from a signed header.
func ecrecover(header *types.Header, sigcache *lru.ARCCache) (common.Address, error) {
	// If the signature's already cached, return that
	hash := header.Hash()
	if address, known := sigcache.Get(hash); known {
		return address.(common.Address), nil
	}
	// Retrieve the signature from the header extra-data
	if len(header.Extra) < extraSeal {
		return common.Address{}, errMissingSignature
	}
	signature := header.Extra[len(header.Extra)-extraSeal:]

	// Recover the public key and the Ethereum address
	pubkey, err := crypto.Ecrecover(SealHash(header).Bytes(), signature)
	if err != nil {
		return common.Address{}, err
	}
	var signer common.Address
	copy(signer[:], crypto.Keccak256(pubkey[1:])[12:])

	sigcache.Add(hash, signer)
	return signer, nil
}

// Clique is the proof-of-authority consensus engine proposed to support the
// Ethereum testnet following the Ropsten attacks.
type Clique struct {
	config *params.ChainConfig // Consensus engine configuration parameters
	db     ethdb.Database      // Database to store and retrieve snapshot checkpoints

	recents    *lru.ARCCache // Snapshots for recent block to speed up reorgs
	signatures *lru.ARCCache // Signatures of recent blocks to speed up mining

	proposals map[common.Address]bool // Current list of proposals we are pushing

	signer types.Signer

	val      common.Address  // Ethereum address of the signing key
	signFn   ctypes.SignerFn // Signer function to authorize hashes with
	signTxFn ctypes.SignerTxFn

	// SuperNodes map[common.Address]struct{} `json:"supernodes"` // Set of authorized bitkub super nodes
	lock sync.RWMutex // Protects the signer fields

	ethAPI EthAPI

	// The fields below are for testing only
	fakeDiff bool // Skip difficulty verifications

	// Contract client
	contractClient ContractClient
}

// New creates a Clique proof-of-authority consensus engine with the initial
// signers set to the ones provided by the user.
func New(
	config *params.ChainConfig,
	db ethdb.Database,
	ethAPI EthAPI,
	contractClient ContractClient,
) *Clique {
	// Set any missing consensus parameters to their defaults
	conf := *config
	if conf.Clique.Epoch == 0 {
		conf.Clique.Epoch = epochLength
	}
	// Allocate the snapshot caches and create the engine
	recents, _ := lru.NewARC(inmemorySnapshots)
	signatures, _ := lru.NewARC(inmemorySignatures)

	defaultSigner := types.NewEIP155Signer(config.ChainID)
	contractClient.SetSigner(defaultSigner)

	return &Clique{
		config:         &conf,
		db:             db,
		recents:        recents,
		signatures:     signatures,
		ethAPI:         ethAPI,
		contractClient: contractClient,
		proposals:      make(map[common.Address]bool),
		signer:         defaultSigner,
	}
}

func (c *Clique) IsSystemTransaction(tx *types.Transaction, header *types.Header, chain consensus.ChainHeaderReader) (bool, error) {
	// deploy a contract
	if tx.To() == nil {
		return false, nil
	}
	sender, err := types.Sender(c.signer, tx)
	if err != nil {
		return false, errors.New("UnAuthorized transaction")
	}
	snap, err := c.snapshot(chain, header.Number.Uint64()-1, header.ParentHash, nil)
	if err != nil {
		return false, errGetSnapshotFailed
	}
	if sender == header.Coinbase && c.isToSystemContract(*tx.To(), snap) && tx.GasPrice().Cmp(big.NewInt(0)) == 0 {
		return true, nil
	}
	return false, nil
}

// Author implements consensus.Engine, returning the Ethereum address recovered
// from the signature in the header's extra-data section.
func (c *Clique) Author(header *types.Header) (common.Address, error) {
	return ecrecover(header, c.signatures)
}

// VerifyHeader checks whether a header conforms to the consensus rules.
func (c *Clique) VerifyHeader(chain consensus.ChainHeaderReader, header *types.Header, seal bool) error {
	return c.verifyHeader(chain, header, nil)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers. The
// method returns a quit channel to abort the operations and a results channel to
// retrieve the async verifications (the order is that of the input slice).
func (c *Clique) VerifyHeaders(chain consensus.ChainHeaderReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := c.verifyHeader(chain, header, headers[:i])

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}

// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.
func (c *Clique) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()

	// Don't waste time checking blocks from the future
	if header.Time > uint64(time.Now().Unix()) {
		return consensus.ErrFutureBlock
	}
	// Checkpoint blocks need to enforce zero beneficiary
	checkpoint := isOnEpochStart(c.config, header.Number)
	if chain.Config().IsErawan(header.Number) {
		voteAddr := c.getVoteAddr(header)
		if checkpoint && voteAddr != (common.Address{}) {
			return errInvalidCheckpointBeneficiary
		}
	} else {
		if checkpoint && header.Coinbase != (common.Address{}) {
			return errInvalidCheckpointBeneficiary
		}
		if header.MixDigest != (common.Hash{}) {
			return errInvalidMixDigest
		}
	}
	// Nonces must be 0x00..0 or 0xff..f, zeroes enforced on checkpoints
	if !bytes.Equal(header.Nonce[:], nonceAuthVote) && !bytes.Equal(header.Nonce[:], nonceDropVote) {
		return errInvalidVote
	}
	if checkpoint && !bytes.Equal(header.Nonce[:], nonceDropVote) {
		return errInvalidCheckpointVote
	}
	// Check that the extra-data contains both the vanity and signature
	if len(header.Extra) < extraVanity {
		return errMissingVanity
	}
	if len(header.Extra) < extraVanity+extraSeal {
		return errMissingSignature
	}

	// Ensure that the extra-data contains a signer list on checkpoint, but none otherwise
	signersBytes := len(header.Extra) - extraVanity - extraSeal

	signerBytesLength := common.AddressLength
	if isNextBlockPoS(c.config, header.Number) {
		checkpoint = needToUpdateValidatorList(c.config, header.Number)
		if checkpoint {
			signerBytesLength = common.AddressLength * 2
			signersBytes -= contractBytesLength
		}
	}

	if !checkpoint && signersBytes != 0 {
		return errExtraSigners
	}

	if checkpoint && signersBytes%signerBytesLength != 0 {
		return errInvalidCheckpointSigners
	}

	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}
	// Ensure that the block's difficulty is meaningful (may not be correct at this point)
	if number > 0 {
		if !c.config.IsChaophraya(header.Number) {
			if header.Difficulty == nil || (!isInturnDifficulty(header.Difficulty) && !isNoturnDifficulty(header.Difficulty)) {
				return errInvalidDifficulty
			}
		}
	}
	// Verify that the gas limit is <= 2^63-1
	if header.GasLimit > params.MaxGasLimit {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, params.MaxGasLimit)
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyForkHashes(chain.Config(), header, false); err != nil {
		return err
	}
	// All basic checks passed, verify cascading fields
	return c.verifyCascadingFields(chain, header, parents)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (c *Clique) verifyCascadingFields(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	// The genesis block is the always valid dead-end
	number := header.Number.Uint64()
	if number == 0 {
		return nil
	}
	// Ensure that the block's timestamp isn't too close to its parent
	var parent *types.Header
	if len(parents) > 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
		return consensus.ErrUnknownAncestor
	}
	if parent.Time+c.config.Clique.Period > header.Time {
		return errInvalidTimestamp
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}
	if !chain.Config().IsLondon(header.Number) {
		// Verify BaseFee not present before EIP-1559 fork.
		if header.BaseFee != nil {
			return fmt.Errorf("invalid baseFee before fork: have %d, want <nil>", header.BaseFee)
		}
		if err := misc.VerifyGaslimit(parent.GasLimit, header.GasLimit); err != nil {
			return err
		}
	} else if err := misc.VerifyEip1559Header(chain.Config(), parent, header); err != nil {
		// Verify the header's EIP-1559 attributes.
		return err
	}
	// Retrieve the snapshot needed to verify this header and cache it
	snap, err := c.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		return err
	}
	// If the block is a checkpoint block, verify the signer list
	if isOnEpochStart(c.config, header.Number) {
		signers := make([]byte, len(snap.Signers)*common.AddressLength)
		for i, val := range snap.signers() {
			copy(signers[i*common.AddressLength:], val[:])
		}
		extraSuffix := len(header.Extra) - extraSeal
		if !c.config.IsChaophraya(header.Number) {
			if !bytes.Equal(header.Extra[extraVanity:extraSuffix], signers) {
				return errMismatchingCheckpointSigners
			}
		}
	}
	// All basic checks passed, verify the seal and return
	if c.config.IsChaophraya(header.Number) {
		return c.verifySealPoS(snap, header, parents)
	}
	return c.verifySeal(snap, header, parents)
}

// snapshot retrieves the authorization snapshot at a given point in time.
func (c *Clique) snapshot(chain consensus.ChainHeaderReader, number uint64, hash common.Hash, parents []*types.Header) (*Snapshot, error) {
	// Search for a snapshot in memory or on disk for checkpoints
	var (
		headers []*types.Header
		snap    *Snapshot
	)
	for snap == nil {
		// If an in-memory snapshot was found, use that
		if s, ok := c.recents.Get(hash); ok {
			snap = s.(*Snapshot)
			break
		}
		// If an on-disk checkpoint snapshot can be found, use that
		if number%checkpointInterval == 0 {
			if s, err := loadSnapshot(c.config, c.signatures, c.db, hash); err == nil {
				log.Trace("Loaded voting snapshot from disk", "number", number, "hash", hash)
				snap = s
				break
			}
		}
		// If we're at the genesis, snapshot the initial state. Alternatively if we're
		// at a checkpoint block without a parent (light client CHT), or we have piled
		// up more headers than allowed to be reorged (chain reinit from a freezer),
		// consider the checkpoint trusted and snapshot it.
		if number == 0 || isOnEpochStart(c.config, new(big.Int).SetUint64(number)) && (len(headers) > params.FullImmutabilityThreshold || chain.GetHeaderByNumber(number-1) == nil) {
			checkpoint := chain.GetHeaderByNumber(number)
			if checkpoint != nil {
				hash := checkpoint.Hash()

				signers := make([]common.Address, (len(checkpoint.Extra)-extraVanity-extraSeal)/common.AddressLength)
				for i := 0; i < len(signers); i++ {
					copy(signers[i][:], checkpoint.Extra[extraVanity+i*common.AddressLength:])
				}
				snap = newSnapshot(c.config, c.signatures, number, hash, signers)
				if err := snap.store(c.db); err != nil {
					return nil, err
				}
				log.Info("Stored checkpoint snapshot to disk", "number", number, "hash", hash)
				break
			}
		}
		// No snapshot for this header, gather the header and move backward
		var header *types.Header
		if len(parents) > 0 {
			// If we have explicit parents, pick from there (enforced)
			header = parents[len(parents)-1]
			if header.Hash() != hash || header.Number.Uint64() != number {
				return nil, consensus.ErrUnknownAncestor
			}
			parents = parents[:len(parents)-1]
		} else {
			// No explicit parents (or no more left), reach out to the database
			header = chain.GetHeader(hash, number)
			if header == nil {
				return nil, consensus.ErrUnknownAncestor
			}
		}
		headers = append(headers, header)
		number, hash = number-1, header.ParentHash
	}

	// Previous snapshot found, apply any pending headers on top of it
	for i := 0; i < len(headers)/2; i++ {
		headers[i], headers[len(headers)-1-i] = headers[len(headers)-1-i], headers[i]
	}

	snap, err := snap.apply(headers, chain, parents, c.config.ChainID)
	if err != nil {
		return nil, err
	}
	c.recents.Add(snap.Hash, snap)

	// If we've generated a new checkpoint snapshot, save to disk
	if td := chain.Config().ChaophrayaBlock; (snap.Number%checkpointInterval == 0 || (td != nil && number == chain.Config().ChaophrayaBlock.Uint64())) && len(headers) > 0 {
		if err = snap.store(c.db); err != nil {
			return nil, err
		}
		log.Trace("Stored voting snapshot to disk", "number", snap.Number, "hash", snap.Hash)
	}

	return snap, err
}

// VerifyUncles implements consensus.Engine, always returning an error for any
// uncles as this consensus mechanism doesn't permit uncles.
func (c *Clique) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

// verifySeal checks whether the signature contained in the header satisfies the
// consensus protocol requirements. The method accepts an optional list of parent
// headers that aren't yet part of the local blockchain to generate the snapshots
// from.
func (c *Clique) verifySeal(snap *Snapshot, header *types.Header, parents []*types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// Resolve the authorization key and check against signers
	signer, err := ecrecover(header, c.signatures)
	if err != nil {
		return err
	}
	if _, ok := snap.Signers[signer]; !ok {
		return errUnauthorizedSigner
	}

	for seen, recent := range snap.Recents {
		if recent == signer {
			// Signer is among recents, only fail if the current block doesn't shift it out
			if limit := uint64(len(snap.Signers)/2 + 1); seen > number-limit {
				return errRecentlySigned
			}
		}
	}

	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !c.fakeDiff {
		inturn := snap.inturn(header.Number.Uint64(), signer)
		if inturn && !isInturnDifficulty(header.Difficulty) {
			return errWrongDifficulty
		}
		if !inturn && !isNoturnDifficulty(header.Difficulty) {
			return errWrongDifficulty
		}
	}
	return nil
}

func (c *Clique) verifySealPoS(snap *Snapshot, header *types.Header, parents []*types.Header) error {
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// Resolve the authorization key and check against signers
	signer, err := ecrecover(header, c.signatures)
	if err != nil {
		return err
	}
	if _, ok := snap.Signers[signer]; !ok && signer != snap.SystemContracts.OfficialNode {
		return errUnauthorizedSigner
	}

	// Ensure that the difficulty corresponds to the turn-ness of the signer
	if !c.fakeDiff {
		inturn := snap.inturn(header.Number.Uint64(), signer)
		if inturn && !isInturnDifficulty(header.Difficulty) {
			return errWrongDifficulty
		}
		if !inturn && !isNoturnDifficulty(header.Difficulty) {
			return errWrongDifficulty
		}
	}
	return nil
}

// Prepare implements consensus.Engine, preparing all the consensus fields of the
// header for running the transactions on top.
func (c *Clique) Prepare(chain consensus.ChainHeaderReader, header *types.Header) error {
	// If the block isn't a checkpoint, cast a random vote (good enough for now)
	number := header.Number.Uint64()
	if !chain.Config().IsErawan(header.Number) {
		header.Coinbase = common.Address{}
	}
	if c.config.IsChaophraya(header.Number) {
		header.Coinbase = c.val
	}
	header.Nonce = types.BlockNonce{}

	// Assemble the voting snapshot to check which votes make sense
	snap, err := c.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}

	if !isOnEpochStart(c.config, header.Number) {
		c.lock.RLock()

		// Gather all the proposals that make sense voting on
		addresses := make([]common.Address, 0, len(c.proposals))
		for address, authorize := range c.proposals {
			if snap.validVote(address, authorize) {
				addresses = append(addresses, address)
			}
		}

		// If there's pending proposals, cast a vote on them
		if len(addresses) > 0 {
			addr := addresses[rand.Intn(len(addresses))]
			if chain.Config().IsErawan(header.Number) {
				header.MixDigest = addr.Hash()
			} else {
				header.Coinbase = addr
			}
			if c.proposals[addr] {
				copy(header.Nonce[:], nonceAuthVote)
			} else {
				copy(header.Nonce[:], nonceDropVote)
			}
		}

		c.lock.RUnlock()
	}
	// Set the correct difficulty
	header.Difficulty = calcDifficulty(snap, c.val)

	// Ensure the extra data has all its components
	if len(header.Extra) < extraVanity {
		header.Extra = append(header.Extra, bytes.Repeat([]byte{0x00}, extraVanity-len(header.Extra))...)
	}
	header.Extra = header.Extra[:extraVanity]

	if isOnEpochStart(c.config, header.Number) {
		if !c.config.IsChaophraya(header.Number) {
			for _, signer := range snap.signers() {
				header.Extra = append(header.Extra, signer[:]...)
			}
		}
	}
	if number > 0 && isNextBlockPoS(c.config, header.Number) {
		if needToUpdateValidatorList(c.config, header.Number) {
			newValidators, systemContracts, err := c.contractClient.GetCurrentValidators(header.ParentHash, new(big.Int).SetUint64(number+1))
			if err != nil {
				log.Error("GetCurrentValidators", "err", err.Error())
				return errors.New("unknown validators")
			}
			for _, validator := range newValidators {
				header.Extra = append(header.Extra, validator.HeaderBytes()...)
			}
			// // Add StakeManager bytes to header.Extra
			header.Extra = append(header.Extra, systemContracts.StakeManager.Bytes()...)
			// // Add SlashManager bytes to header.Extra
			header.Extra = append(header.Extra, systemContracts.SlashManager.Bytes()...)
			// // Add OfficialNode bytes to header.Extra
			header.Extra = append(header.Extra, systemContracts.OfficialNode.Bytes()...)
		}
	}

	// Ensure the timestamp has the correct delay
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}

	header.Extra = append(header.Extra, make([]byte, extraSeal)...)

	header.Time = parent.Time + c.config.Clique.Period
	if header.Time < uint64(time.Now().Unix()) {
		header.Time = uint64(time.Now().Unix())
	}
	return nil
}

func ParseAddressBytes(b []byte) ([]*common.Address, error) {
	if len(b)%20 != 0 {
		return nil, errors.New("invalid address bytes")
	}
	result := make([]*common.Address, len(b)/20)
	for i := 0; i < len(b); i += 20 {
		address := make([]byte, 20)
		copy(address, b[i:i+20])
		addr := common.BytesToAddress(address)
		result[i/20] = &addr
	}
	return result, nil
}

// Finalize implements consensus.Engine, ensuring no uncles are set, nor block
// rewards given.
func (c *Clique) Finalize(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB, txs *[]*types.Transaction,
	uncles []*types.Header, receipts *[]*types.Receipt, systemTxs *[]*types.Transaction, usedGas *uint64) error {

	if c.config.IsChaophraya(header.Number) {

		if c.config.ChaophrayaBlock.Cmp(header.Number) == 0 {
			log.Info("⭐️ POS Started", "number", header.Number)
		}

		snap, err := c.snapshot(chain, header.Number.Uint64()-1, header.ParentHash, nil)
		if err != nil {
			panic(err)
		}
		number := header.Number.Uint64()
		blockSigner, _ := ecrecover(header, c.signatures)
		if isNoturnDifficulty(header.Difficulty) && blockSigner != snap.SystemContracts.OfficialNode {
			return errInvalidDifficulty
		}

		if needToUpdateValidatorList(c.config, header.Number) {
			newValidators, _, err := c.contractClient.GetCurrentValidators(header.ParentHash, new(big.Int).SetUint64(number+1))
			if err != nil {
				return err
			}

			validatorsBytes := make([]byte, len(newValidators)*validatorBytesLength)
			for i, validator := range newValidators {
				copy(validatorsBytes[i*validatorBytesLength:], validator.HeaderBytes())
			}

			extraSuffix := len(header.Extra) - extraSeal - contractBytesLength
			if !bytes.Equal(header.Extra[extraVanity:extraSuffix], validatorsBytes) {
				return errMismatchingSpanValidators
			}
		}

		cx := chainContext{Chain: chain, clique: c}

		if isSpanCommitmentBlock(c.config, header.Number) {
			err := c.commitSpan(c.val, state, header, cx, txs, receipts, systemTxs, usedGas, false)
			if err != nil {
				return errInvalidSpan
			}
		}

		// noturn is only permitted from official node
		if !isInturnDifficulty(header.Difficulty) && header.Coinbase != snap.SystemContracts.OfficialNode {
			return errUnauthorizedSigner
		}

		// Begin slashing state update
		if !isInturnDifficulty(header.Difficulty) && header.Coinbase == snap.SystemContracts.OfficialNode {
			log.Debug("ℹ️  Commited by official node", "validator", header.Coinbase, "diff", header.Difficulty, "number", header.Number)
			inturnSigner := snap.getInturnSigner(header.Number.Uint64())
			log.Debug("🗡️  Slashing validator", "signer", inturnSigner, "diff", header.Difficulty, "number", header.Number)
			err = c.slash(inturnSigner, chain, state, header, cx, txs, receipts, systemTxs, usedGas, false, snap)
			if err != nil {
				return err
			}
		}

		val := header.Coinbase
		err = c.distributeIncoming(val, state, header, cx, txs, receipts, systemTxs, usedGas, false, snap)
		if err != nil {
			return err
		}
		if len(*systemTxs) > 0 {
			return errors.New("the length of systemTxs do not match")
		}

	}
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)

	return nil
}

// FinalizeAndAssemble implements consensus.Engine, ensuring no uncles are set,
// nor block rewards given, and returns the final block.
func (c *Clique) FinalizeAndAssemble(chain consensus.ChainHeaderReader, header *types.Header, state *state.StateDB,
	txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, []*types.Receipt, error) {
	if c.config.IsChaophraya(header.Number) {
		snap, err := c.snapshot(chain, header.Number.Uint64()-1, header.ParentHash, nil)
		if err != nil {
			panic(err)
		}
		cx := chainContext{Chain: chain, clique: c}
		if txs == nil {
			txs = make([]*types.Transaction, 0)
		}
		if receipts == nil {
			receipts = make([]*types.Receipt, 0)
		}
		if isSpanCommitmentBlock(c.config, header.Number) {
			err := c.commitSpan(c.val, state, header, cx, &txs, &receipts, nil, &header.GasUsed, true)
			if err != nil {
				return nil, nil, errInvalidSpan
			}
		}
		// Begin slashing
		if !isInturnDifficulty(header.Difficulty) && header.Coinbase == snap.SystemContracts.OfficialNode {
			inturnSigner := snap.getInturnSigner(header.Number.Uint64())
			log.Debug("🗡️  Slashing validator (FAA)", "signer", inturnSigner, "diff", header.Difficulty, "number", header.Number)
			err = c.slash(inturnSigner, chain, state, header, cx, &txs, &receipts, nil, &header.GasUsed, true, snap)
			if err != nil {
				return nil, nil, err
			}

		}
		err = c.distributeIncoming(c.val, state, header, cx, &txs, &receipts, nil, &header.GasUsed, true, snap)
		if err != nil {
			return nil, nil, err
		}

	}
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))
	header.UncleHash = types.CalcUncleHash(nil)

	// Assemble and return the final block for sealing
	return types.NewBlock(header, txs, nil, receipts, trie.NewStackTrie(nil)), receipts, nil
}

// slash spoiled validators
func (c *Clique) slash(spoiledVal common.Address, chain consensus.ChainHeaderReader, state *state.StateDB, header *types.Header, cx core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, snap *Snapshot) error {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	currentSpan, err := c.contractClient.GetCurrentSpan(ctx, header)
	if err != nil {
		return err
	}
	if isSpanFirstBlock(c.config, header.Number) {
		currentSpan = new(big.Int).Add(currentSpan, common.Big1)
	}

	slashed, err := c.contractClient.IsSlashed(snap.SystemContracts.SlashManager, chain, spoiledVal, currentSpan, header)

	if err != nil {
		return err
	}
	// ignore slash
	if slashed {
		return nil
	}

	return c.contractClient.Slash(snap.SystemContracts.SlashManager, spoiledVal, chain, state, header, cx, txs, receipts, receivedTxs, usedGas, mining, currentSpan)

}

func (c *Clique) distributeIncoming(val common.Address, state *state.StateDB, header *types.Header, chain core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool, snap *Snapshot) error {
	coinbase := header.Coinbase
	balance := state.GetBalance(consensus.SystemAddress)
	if balance.Cmp(common.Big0) <= 0 {
		return nil
	}
	state.SetBalance(consensus.SystemAddress, big.NewInt(0))
	state.AddBalance(coinbase, balance)

	log.Debug("distribute to validator contract", "block hash", header.Hash(), "amount", balance)
	return c.contractClient.DistributeToValidator(snap.SystemContracts.StakeManager, balance, val, state, header, chain, txs, receipts, receivedTxs, usedGas, mining)
}

func (c *Clique) commitSpan(val common.Address, state *state.StateDB, header *types.Header, chain core.ChainContext,
	txs *[]*types.Transaction, receipts *[]*types.Receipt, receivedTxs *[]*types.Transaction, usedGas *uint64, mining bool) error {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)

	confirmBlockNr, _ := c.ethAPI.GetHeaderTypeByNumber(ctx, rpc.BlockNumber(parent.Number.Uint64()-5))

	newValidators, _ := c.selectNextValidatorSet(parent, confirmBlockNr)

	// get validators bytes
	var validators []ctypes.MinimalVal
	for _, val := range newValidators {
		validators = append(validators, val.MinimalVal())
	}
	validatorBytes, _ := rlp.EncodeToBytes(validators)

	return c.contractClient.CommitSpan(val, state, header, chain, txs, receipts, receivedTxs, usedGas, mining, validatorBytes)
}

// Authorize injects a private key into the consensus engine to mint new blocks
// with.
func (c *Clique) Authorize(val common.Address, signFn ctypes.SignerFn, signTxFn ctypes.SignerTxFn) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.val = val
	c.signFn = signFn
	c.signTxFn = signTxFn
	c.contractClient.Inject(c.val, c.signTxFn)
}

// Seal implements consensus.Engine, attempting to create a sealed block using
// the local signing credentials.
func (c *Clique) Seal(chain consensus.ChainHeaderReader, block *types.Block, results chan<- *types.Block, stop <-chan struct{}) error {
	header := block.Header()

	// Sealing the genesis block is not supported
	number := header.Number.Uint64()

	if number == 0 {
		return errUnknownBlock
	}
	// For 0-period chains, refuse to seal empty blocks (no reward but would spin sealing)
	if c.config.Clique.Period == 0 && len(block.Transactions()) == 0 {
		return errors.New("sealing paused while waiting for transactions")
	}
	// Don't hold the signer fields for the entire sealing procedure
	c.lock.RLock()
	val, signFn := c.val, c.signFn
	c.lock.RUnlock()

	// Bail out if we're unauthorized to sign a block
	snap, err := c.snapshot(chain, number-1, header.ParentHash, nil)
	if err != nil {
		return err
	}
	if !c.config.IsChaophraya(header.Number) {
		if _, authorized := snap.Signers[val]; !authorized {
			return errUnauthorizedSigner
		}
	}
	if c.config.IsChaophraya(header.Number) {
		if _, authorized := snap.Signers[val]; !authorized && val != snap.SystemContracts.OfficialNode {
			return errUnauthorizedSigner
		}
	}

	// If we're amongst the recent signers, wait for the next block
	if !c.config.IsChaophraya(header.Number) {
		for seen, recent := range snap.Recents {
			if recent == val {
				// Signer is among recents, only wait if the current block doesn't shift it out
				if limit := uint64(len(snap.Signers)/2 + 1); number < limit || seen > number-limit {
					return errors.New("signed recently, must wait for others")
				}
			}
		}
	}

	// Sweet, the protocol permits us to sign the block, wait for our time
	delay := time.Unix(int64(header.Time), 0).Sub(time.Now()) // nolint: gosimple
	// Only be used in PoS
	slashed := false
	// TODO: Implement the backup plan in case all validator nodes are down,
	// We propose the official validator node which operate by Bitkub Blockchain Technology Co., Ltd.
	// 1. The super node will be the right validator node to seal the block incase of the inturn validator node does not propagate the block in time.
	// The timing of delay, the official will operate to sealing the block and propagate after 1 sec of delay.
	if !c.config.IsChaophraya(header.Number) {
		if isNoturnDifficulty(header.Difficulty) {
			// It's not our turn explicitly to sign, delay it a bit
			wiggle := time.Duration(len(snap.Signers)/2+1) * wiggleTime
			delay += time.Duration(rand.Int63n(int64(wiggle)))

			log.Trace("Out-of-turn signing requested", "wiggle", common.PrettyDuration(wiggle))
		}
	} else {
		if isNoturnDifficulty(header.Difficulty) {
			delay += time.Duration(rand.Int63n(int64(wiggleTime)))
		}
		ctx := context.Background()
		inturnSigner := snap.getInturnSigner(header.Number.Uint64())
		currentSpan, err := c.contractClient.GetCurrentSpan(ctx, header)
		if err != nil {
			return err
		}
		if isSpanFirstBlock(c.config, header.Number) {
			currentSpan = new(big.Int).Add(currentSpan, common.Big1)
		}
		slashed, err = c.contractClient.IsSlashed(snap.SystemContracts.SlashManager, chain, inturnSigner, currentSpan, header)
		if err != nil {
			return err
		}
	}

	// Sign all the things!
	sighash, err := signFn(accounts.Account{Address: val}, accounts.MimetypeClique, CliqueRLP(header))
	if err != nil {
		return err
	}

	copy(header.Extra[len(header.Extra)-extraSeal:], sighash)
	// Wait until sealing is terminated or delay timeout.
	log.Trace("Waiting for slot to sign and propagate", "delay", common.PrettyDuration(delay))

	go func() {
		select {
		case <-stop:
			return
		case <-time.After(delay):
		}
		if c.config.IsChaophraya(header.Number) && (!isInturnDifficulty(header.Difficulty) || slashed) {
			defaultWaitTime := time.Duration(2)
			if slashed {
				defaultWaitTime = time.Duration(0)
			}
			select {
			case <-stop:
				return
			case <-time.After(defaultWaitTime * time.Second):
				if val != snap.SystemContracts.OfficialNode {
					<-stop
					return
				}
			}
		}

		select {
		case results <- block.WithSeal(header):
		default:
			log.Warn("Sealing result is not read by miner", "sealhash", SealHash(header))
		}
	}()
	return nil
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns the difficulty
// that a new block should have:“
// * DIFF_NOTURN(2) if BLOCK_NUMBER % SIGNER_COUNT != SIGNER_INDEX
// * DIFF_INTURN(1) if BLOCK_NUMBER % SIGNER_COUNT == SIGNER_INDEX
func (c *Clique) CalcDifficulty(chain consensus.ChainHeaderReader, time uint64, parent *types.Header) *big.Int {
	snap, err := c.snapshot(chain, parent.Number.Uint64(), parent.Hash(), nil)
	if err != nil {
		return nil
	}
	return calcDifficulty(snap, c.val)
}

func calcDifficulty(snap *Snapshot, signer common.Address) *big.Int {
	if snap.inturn(snap.Number+1, signer) {
		return new(big.Int).Set(diffInTurn)
	}
	return new(big.Int).Set(diffNoTurn)
}

// SealHash returns the hash of a block prior to it being sealed.
func (c *Clique) SealHash(header *types.Header) common.Hash {
	return SealHash(header)
}

// Close implements consensus.Engine. It's a noop for clique as there are no background threads.
func (c *Clique) Close() error {
	return nil
}

// APIs implements consensus.Engine, returning the user facing RPC API to allow
// controlling the signer voting.
func (c *Clique) APIs(chain consensus.ChainHeaderReader) []rpc.API {
	return []rpc.API{{
		Namespace: "clique",
		Version:   "1.0",
		Service:   &API{chain: chain, clique: c},
		Public:    false,
	}}
}

func (c *Clique) selectNextValidatorSet(parent *types.Header, seedBlock *types.Header) ([]ctypes.Validator, error) {
	selectedProducers := make([]ctypes.Validator, 0)

	// seed hash will be from parent hash to seed block hash
	seedBytes := ToBytes32(seedBlock.Hash().Bytes()[:32])
	seed := int64(binary.BigEndian.Uint64(seedBytes[:]))

	r := rand.New(rand.NewSource(seed))

	newValidators, _ := c.contractClient.GetEligibleValidators(parent.Hash(), parent.Number.Uint64())

	// weighted range from validators' voting power
	votingPower := make([]uint64, len(newValidators))
	for idx, validator := range newValidators {
		votingPower[idx] = uint64(validator.VotingPower)
	}

	weightedRanges, totalVotingPower := createWeightedRanges(votingPower)

	for i := uint64(0); i < c.config.Clique.Span; i++ {
		/*
			random must be in [1, totalVotingPower] to avoid situation such as
			2 validators with 1 staking power each.
			Weighted range will look like (1, 2)
			Rolling inclusive will have a range of 0 - 2, making validator with staking power 1 chance of selection = 66%
		*/
		targetWeight := randomRangeInclusive(1, totalVotingPower, r)
		index := binarySearch(weightedRanges, targetWeight)
		selectedProducers = append(selectedProducers, *newValidators[index])
	}
	return selectedProducers[:c.config.Clique.Span], nil
}

func binarySearch(array []uint64, search uint64) int {
	if len(array) == 0 {
		return -1
	}
	l := 0
	r := len(array) - 1
	for l < r {
		mid := (l + r) / 2
		if array[mid] >= search {
			r = mid
		} else {
			l = mid + 1
		}
	}
	return l
}

// randomRangeInclusive produces unbiased pseudo random in the range [min, max]. Uses rand.Uint64() and can be seeded beforehand.
func randomRangeInclusive(min uint64, max uint64, r *rand.Rand) uint64 {
	if max <= min {
		return max
	}

	rangeLength := max - min + 1
	maxAllowedValue := math.MaxUint64 - math.MaxUint64%rangeLength - 1
	randomValue := r.Uint64()

	// reject anything that is beyond the reminder to avoid bias
	for randomValue >= maxAllowedValue {
		randomValue = r.Uint64()
	}

	return min + randomValue%rangeLength
}

// createWeightedRanges converts array [1, 2, 3] into cumulative form [1, 3, 6]
func createWeightedRanges(weights []uint64) ([]uint64, uint64) {
	weightedRanges := make([]uint64, len(weights))
	totalWeight := uint64(0)
	for i := 0; i < len(weightedRanges); i++ {
		totalWeight += weights[i]
		weightedRanges[i] = totalWeight
	}
	return weightedRanges, totalWeight
}

func ToBytes32(x []byte) [32]byte {
	var y [32]byte
	copy(y[:], x)
	return y
}

// SealHash returns the hash of a block prior to it being sealed.
func SealHash(header *types.Header) (hash common.Hash) {
	hasher := sha3.NewLegacyKeccak256()
	encodeSigHeader(hasher, header)
	hasher.(crypto.KeccakState).Read(hash[:])
	return hash
}

// CliqueRLP returns the rlp bytes which needs to be signed for the proof-of-authority
// sealing. The RLP to sign consists of the entire header apart from the 65 byte signature
// contained at the end of the extra data.
//
// Note, the method requires the extra data to be at least 65 bytes, otherwise it
// panics. This is done to avoid accidentally using both forms (signature present
// or not), which could be abused to produce different hashes for the same header.
func CliqueRLP(header *types.Header) []byte {
	b := new(bytes.Buffer)
	encodeSigHeader(b, header)
	return b.Bytes()
}

func encodeSigHeader(w io.Writer, header *types.Header) {
	enc := []interface{}{
		header.ParentHash,
		header.UncleHash,
		header.Coinbase,
		header.Root,
		header.TxHash,
		header.ReceiptHash,
		header.Bloom,
		header.Difficulty,
		header.Number,
		header.GasLimit,
		header.GasUsed,
		header.Time,
		header.Extra[:len(header.Extra)-crypto.SignatureLength], // Yes, this will panic if extra is too short
		header.MixDigest,
		header.Nonce,
	}
	if header.BaseFee != nil {
		enc = append(enc, header.BaseFee)
	}
	if err := rlp.Encode(w, enc); err != nil {
		panic("can't encode: " + err.Error())
	}
}

func (c *Clique) getVoteAddr(header *types.Header) common.Address {
	if c.config.IsErawan(header.Number) {
		if big.NewInt(0).SetBytes(header.MixDigest[(common.HashLength-common.AddressLength):(common.HashLength-common.AddressLength)]).Cmp(common.Big0) == 0 {
			return common.BytesToAddress(header.MixDigest[(common.HashLength - common.AddressLength):])
		}
		return common.Address{}
	} else {
		return header.Coinbase
	}
}

// chain context
type chainContext struct {
	Chain  consensus.ChainHeaderReader
	clique consensus.Engine
}

func (c chainContext) Engine() consensus.Engine {
	return c.clique
}

func (c chainContext) GetHeader(hash common.Hash, number uint64) *types.Header {
	return c.Chain.GetHeader(hash, number)
}

func (c chainContext) Config() *params.ChainConfig {
	return c.Chain.Config()
}

// Check whether the given block is in the first block of an epoch
func isOnEpochStart(config *params.ChainConfig, number *big.Int) bool {
	n := number.Uint64()
	return n%config.Clique.Epoch == 0
}

// Check whether the next block of the given block is in proof-of-stake period.
func isNextBlockPoS(config *params.ChainConfig, number *big.Int) bool {
	return config.IsChaophraya(new(big.Int).Add(number, common.Big1))
}

// Check whether the given block is the commitment block (mid-span).
func isSpanCommitmentBlock(config *params.ChainConfig, number *big.Int) bool {
	bigSpan := new(big.Int).SetUint64(config.Clique.Span)

	// number % span
	mod := new(big.Int).Mod(number, bigSpan)
	// span / 2 + 1
	midSpan := new(big.Int).Div(bigSpan, common.Big2)
	midSpan = midSpan.Add(midSpan, common.Big1)

	// is pos && number % span = span / 2 + 1
	return config.IsChaophraya(number) && mod.Cmp(midSpan) == 0
}

// Check whether the given block is the first block of the span.
func isSpanFirstBlock(config *params.ChainConfig, number *big.Int) bool {
	bigSpan := new(big.Int).SetUint64(config.Clique.Span)
	mod := new(big.Int).Mod(number, bigSpan)
	return config.IsChaophraya(number) && mod.Cmp(common.Big0) == 0
}

// Check whether the next block of the given block is the first block of the span.
func isNextBlockASpanFirstBlock(config *params.ChainConfig, number *big.Int) bool {
	bigSpan := new(big.Int).SetUint64(config.Clique.Span)
	nextBlock := new(big.Int).Add(number, common.Big1)
	// (number + 1) % span
	mod := new(big.Int).Mod(nextBlock, bigSpan)
	// is pos && (number + 1) % span == 0
	return config.IsChaophraya(nextBlock) && mod.Cmp(common.Big0) == 0
}

// Check whether geth should update the validator list or not
func needToUpdateValidatorList(config *params.ChainConfig, number *big.Int) bool {
	return isNextBlockASpanFirstBlock(config, number) || isNextBlockExactChaophrayaBlock(config, number)
}

func isNextBlockExactChaophrayaBlock(config *params.ChainConfig, number *big.Int) bool {
	nextBlock := new(big.Int).Add(number, common.Big1)
	return config.IsChaophraya(nextBlock) && config.ChaophrayaBlock.Cmp(nextBlock) == 0
}

// Check whether the given difficulty is the inturn difficulty.
func isInturnDifficulty(diff *big.Int) bool {
	return diff.Cmp(diffInTurn) == 0
}

// Check whether the given difficulty is the noturn difficulty.
func isNoturnDifficulty(diff *big.Int) bool {
	return diff.Cmp(diffNoTurn) == 0
}
