# Changelog

## v1.1.0-bkc (Erawan)
### Description

v1.1.0-bkc is a **hard fork** release.

v1.1.0-bkc brings **Erawan hard fork** which changes the consensus mechanism from **POA** to **POSA**. The Erawan hard fork is scheduled to occur at block #0000000 on the Bitkub Chain Mainnet (~ August 4th, 2021).

**Users of Bitkubchain must upgrade to this release before the Erawan hard-fork activates to remain in consensus**.

### Changes
* Add Erawan hard fork chain configuration and checkpoint function
* Propose the new flag ```miner.sealerAddress``` for sealing the block instead of etherbase.
* Use flag ```miner.etherbase``` as a beneficiary address 
* Modify block header, using ```miner``` to record and track the beneficiary address
* Use ```Mix.Digest``` instead of ```Coinbase``` for voting signer
* Use ```Coinbase```(inherited from miner.etherbase) for store the beneficiary address (the address that received the block reward)

***Note***: All of the changes will be backward compatible ith official go-ethereum until block number reach the ```erawanBlock```. Node runner both Validator node and RPC node must upgrade their geth binary and re-initialize genesis file (erawanBlock enabled) before ```erawanBlock``` to prevent the BAD Blockw
