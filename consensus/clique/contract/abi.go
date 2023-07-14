package contract

const stakeManageABI = `[
  {
    "inputs": [],
    "name": "distributeReward",
    "outputs": [],
    "stateMutability": "payable",
    "type": "function"
  }
]`
const validatorSetABI = `[
  {
    "inputs": [
      {
        "internalType": "bytes",
        "name": "validatorBytes_",
        "type": "bytes"
      }
    ],
    "name": "commitSpan",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "currentSpanNumber",
    "outputs": [
      {
        "internalType": "uint256",
        "name": "",
        "type": "uint256"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "getEligibleValidators",
    "outputs": [
      {
        "components": [
          {
            "internalType": "address",
            "name": "signer",
            "type": "address"
          },
          {
            "internalType": "uint256",
            "name": "power",
            "type": "uint256"
          }
        ],
        "internalType": "struct MinimalValidator[]",
        "name": "",
        "type": "tuple[]"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [
      {
        "internalType": "uint256",
        "name": "number_",
        "type": "uint256"
      }
    ],
    "name": "getValidators",
    "outputs": [
      {
        "internalType": "address[]",
        "name": "",
        "type": "address[]"
      },
      {
        "internalType": "uint256[]",
        "name": "",
        "type": "uint256[]"
      },
      {
        "internalType": "address[3]",
        "name": "",
        "type": "address[3]"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  }
]`
const slashABI = `[
  {
    "inputs": [
      {
        "internalType": "address",
        "name": "signer_",
        "type": "address"
      },
      {
        "internalType": "uint256",
        "name": "span_",
        "type": "uint256"
      }
    ],
    "name": "isSignerSlashed",
    "outputs": [
      {
        "internalType": "bool",
        "name": "",
        "type": "bool"
      }
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [
      {
        "internalType": "address",
        "name": "signer_",
        "type": "address"
      },
      {
        "internalType": "uint256",
        "name": "currentSpan_",
        "type": "uint256"
      }
    ],
    "name": "slash",
    "outputs": [
      {
        "internalType": "bool",
        "name": "",
        "type": "bool"
      }
    ],
    "stateMutability": "nonpayable",
    "type": "function"
  }
]`
