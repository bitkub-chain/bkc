# Bitkub Chain
Bitkub Chain is an infrastructure of an ecosystem using decentralized technology “Blockchain” which allows anyone to interact with decentralized applications or their digital assets with a very low transaction fee, high-speed confirmation time, trustless and  transparency to everyone.

Bitkub Chain is a fork of go-ethereum. When the chain starts, it adopts Proof of Authority (PoA) consensus mechanism to allow short block time. Bitkub Chain will soon introduce Proof of Staked Authority (PoSA) according to the published [whitepaper](https://www.bitkubchain.com/docs/EN_Bitkub_Chain_WhitePaper_V2.1.pdf).

## Proof of Staked Authority

The PoSA of Bitkub Chain comprises of two parts. The first part is a set of smart contract which handle staking and distributing reward. The second part is an extended version of go-ethereum's Clique consensus (PoA) to make it able to send block's reward to the smart contract.

## Compile the binary

Building `geth` requires both a Go (version 1.17 or later) and a C compiler. You can install them using your favourite package manager.

* Install Go

```shell
rm -rf /usr/local/go && curl -L https://golang.google.cn/dl/go1.17.6.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH=$PATH:/usr/local/go/bin
```

* Clone bkc repository

```shell
# Will be changed to github
git clone https://gitlab.com/bitkub-blockchain/bitkub-chain/bkc.git && cd bkc
```
* Build geth binary

```shell
make geth
mv ./build/bin/geth /usr/local/bin/
```

## Run from the binary
The binary for a linux x86 architecture is provided in [release](https://github.com/bitkub-blockchain/bkc/releases/tag/v1.1.0-bkc)


## Executables

The bkc project comes with executable found in the `cmd` directory.

|    Command    | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| :-----------: | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
|  **`geth`**   | Our Bitkub Chain CLI client. It is the entry point into the BKC network (main-, test- or private net), capable of running as a full node (default), archive node (retaining all historical state) or a light node (retrieving data live). It can be used by other processes as a gateway into the BKC network via JSON RPC endpoints exposed on top of HTTP, WebSocket and/or IPC transports. `geth --help` and the [CLI page](https://geth.ethereum.org/docs/interface/command-line-options) for command line options.          |

## Running `geth`

Going through all the possible command line flags is out of scope here (please consult our
[CLI Wiki page](https://geth.ethereum.org/docs/interface/command-line-options)),
but we've enumerated a few common parameter combos to get you up to speed quickly
on how you can run your own `geth` instance.

### Hardware Requirements (recommended)
- 12 vCPUs
- 48 GiB of RAM
- 1 TB of Data Disk (SSD) IOPS: 20,000
- Suggest m5zn.3xlarge instance type on AWS, or c2-standard-16 on Google cloud.
- A broadband Internet connection with upload/download speeds of at least 50 megabyte per second

#### Security Group & Firewall Rules
##### Allow Inbound
- Protocol - TCP & UDP
- Port - 30303
- Source IP - 0.0.0.0/0

##### Network Bandwidth
- 50 Mbps/Sec
 

```shell
$ geth console
```

This command will:
 * Start `geth` in snap sync mode (default, can be changed with the `--syncmode` flag), causing it to download more data in exchange for avoiding processing the entire history of the Bitkub Chain network, which is very CPU intensive.
 * Start up `geth`'s built-in interactive [JavaScript console](https://geth.ethereum.org/docs/interface/javascript-console), (via the trailing `console` subcommand) through which you can interact using [`web3` methods](https://github.com/ChainSafe/web3.js/blob/0.20.7/DOCUMENTATION.md) (note: the `web3` version bundled within `geth` is very old, and not up to date with official docs), as well as `geth`'s own [management APIs](https://geth.ethereum.org/docs/rpc/server). This tool is optional and if you leave it out you can always attach to an already running `geth` instance with `geth attach`.

### A node on the Bitkub Chain network
Download the following binary, configuration and genesis files according to the mainnet or the testnet.

### Download chain configurations
* Mainnet
    * config.toml
    ```shell
    wget https://raw.githubusercontent.com/bitkub-blockchain/bkc-node/main/mainnet/config.toml
    ```
    * genesis.json
    ```shell
    wget https://raw.githubusercontent.com/bitkub-blockchain/bkc-node/main/mainnet/genesis.json
    ```
* Testnet
     * config.toml
    ```shell
    wget https://raw.githubusercontent.com/bitkub-blockchain/bkc-node/main/testnet/config.toml
    ```
    * genesis.json
    ```shell
    wget https://raw.githubusercontent.com/bitkub-blockchain/bkc-node/main/testnet/genesis.json
    ```
    
### RPC node running guideline

* Initialize a genesis file
```shell
geth init --datadir { PATH } genesis.json
```

* Run node
```shell
geth --datadir { PATH } --config config.toml { any additional flags }
```

### Validator node running guideline

* Initialize a genesis file
```shell
geth init --datadir { PATH } genesis.json
```

* Run node

Before running `geth` command, make sure the reward address was set correctly. In the new `geth` binary that included the Erawan hard fork. The owner of the validator node can set either wallet address or smart contract address as a beneficially address by adding the flag `miner.etherbase`.

|    Flag    | Required | Default |
| - | :-: | - |
|`--miner.sealerAddress`| ❌ | First unlocked account |
|`--miner.etherbase`| ❌ | First unlocked account |

```shell
geth --datadir { PATH } --config config.toml --miner.etherbase { wallet/smart contract address} { any additional flags }
```

### Configuration

As an alternative to passing the numerous flags to the `geth` binary, you can also pass a
configuration file via:

```shell
$ geth --config /path/to/your_config.toml
```

To get an idea how the file should look like you can use the `dumpconfig` subcommand to
export your existing configuration:

```shell
$ geth --your-favourite-flags dumpconfig
```

### Programmatically interfacing `geth` nodes

As a developer, sooner rather than later you'll want to start interacting with `geth` and the Bitkub Chain network via your own programs and not manually through the console. To aid this, `geth` has built-in support for a JSON-RPC based APIs ([standard APIs](https://eth.wiki/json-rpc/API) and [`geth` specific APIs](https://geth.ethereum.org/docs/rpc/server)). These can be exposed via HTTP, WebSockets and IPC (UNIX sockets on UNIX based platforms, and named pipes on Windows).

The IPC interface is enabled by default and exposes all the APIs supported by `geth`,
whereas the HTTP and WS interfaces need to manually be enabled and only expose a
subset of APIs due to security reasons. These can be turned on/off and configured as
you'd expect.

HTTP based JSON-RPC API options:

  * `--http` Enable the HTTP-RPC server
  * `--http.addr` HTTP-RPC server listening interface (default: `localhost`)
  * `--http.port` HTTP-RPC server listening port (default: `8545`)
  * `--http.api` API's offered over the HTTP-RPC interface (default: `eth,net,web3`)
  * `--http.corsdomain` Comma separated list of domains from which to accept cross origin requests (browser enforced)
  * `--ws` Enable the WS-RPC server
  * `--ws.addr` WS-RPC server listening interface (default: `localhost`)
  * `--ws.port` WS-RPC server listening port (default: `8546`)
  * `--ws.api` API's offered over the WS-RPC interface (default: `eth,net,web3`)
  * `--ws.origins` Origins from which to accept websockets requests
  * `--ipcdisable` Disable the IPC-RPC server
  * `--ipcapi` API's offered over the IPC-RPC interface (default: `admin,debug,eth,miner,net,personal,shh,txpool,web3`)
  * `--ipcpath` Filename for IPC socket/pipe within the datadir (explicit paths escape it)

You'll need to use your own programming environments' capabilities (libraries, tools, etc) to
connect via HTTP, WS or IPC to a `geth` node configured with the above flags and you'll
need to speak [JSON-RPC](https://www.jsonrpc.org/specification) on all transports. You
can reuse the same connection for multiple requests!

**Note: Please understand the security implications of opening up an HTTP/WS based
transport before doing so! Hackers on the internet are actively trying to subvert BKC nodes with exposed APIs! Further, all browser tabs can access locally
running web servers, so malicious web pages could try to subvert locally available
APIs!**


## Contribution

Thank you for considering to help out with the source code! We welcome contributions
from anyone on the internet, and are grateful for even the smallest of fixes!

If you'd like to contribute to go-ethereum, please fork, fix, commit and send a pull request
for the maintainers to review and merge into the main code base. If you wish to submit
more complex changes though, please check up with the core devs first on [our Discord Server](https://discord.gg/invite/nthXNEv)
to ensure those changes are in line with the general philosophy of the project and/or get
some early feedback which can make both your efforts much lighter as well as our review
and merge procedures quick and simple.

Please make sure your contributions adhere to our coding guidelines:

 * Code must adhere to the official Go [formatting](https://golang.org/doc/effective_go.html#formatting)
   guidelines (i.e. uses [gofmt](https://golang.org/cmd/gofmt/)).
 * Code must be documented adhering to the official Go [commentary](https://golang.org/doc/effective_go.html#commentary)
   guidelines.
 * Pull requests need to be based on and opened against the `master` branch.
 * Commit messages should be prefixed with the package(s) they modify.
   * E.g. "eth, rpc: make trace configs optional"

Please see the [Developers' Guide](https://geth.ethereum.org/docs/developers/devguide)
for more details on configuring your environment, managing project dependencies, and
testing procedures.

## License

The bkc library (i.e. all code outside of the `cmd` directory) is licensed under the
[GNU Lesser General Public License v3.0](https://www.gnu.org/licenses/lgpl-3.0.en.html),
also included in our repository in the `COPYING.LESSER` file.

The bkc binaries (i.e. all code inside of the `cmd` directory) is licensed under the
[GNU General Public License v3.0](https://www.gnu.org/licenses/gpl-3.0.en.html), also
included in our repository in the `COPYING` file.