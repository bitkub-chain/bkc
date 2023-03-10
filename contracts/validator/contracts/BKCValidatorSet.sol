// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract BKCValidatorSet {
    /*************
     * Variables *
     *************/
    mapping(address => Validator) public validatorSetMap;
    mapping(address =>uint256) public currentValidatorSetMap;
    address[] public validators;
    address public owner;
    uint256 index = 1;
    uint256 public totalInComing;

    struct Validator{
        // only in state
        // uint64  votingPower;
        bool active;
        uint256 incoming;
    }

    /**********
     * Events *
     **********/
    event AddValidator();
    event RemoveValidator();
    event validatorDeposit(address indexed validator, uint256 amount);


    /***************
     * Constructor *
     ***************/

    constructor() {
        owner = msg.sender;
    }

    /*********************** modifiers **************************/
    modifier noEmptyDeposit() {
        require(msg.value > 0, "deposit value is zero");
        _;
    }
    modifier onlyCoinbase() {
        require(msg.sender == block.coinbase, "the message sender must be the block producer");
        _;
    }

    function init() public {
        // require(msg.sender == owner, "Only owner can initialize the contract");
        // first validator set
        addValidator(0x48F30fb9B69454b09f8b4691412Cf4aa3753fcB1);
        addValidator(0xB05936175536F920B7fC96CCEb24Fecd7BB7F8F8);
        currentValidatorSetMap[0x48F30fb9B69454b09f8b4691412Cf4aa3753fcB1] = 1;
        currentValidatorSetMap[0xB05936175536F920B7fC96CCEb24Fecd7BB7F8F8] = 2;
        index = 3;
        emit AddValidator();
        // validators.push(0x96C9F2F893AdeF66669B4bB4A7dfA5006c037CD3);
    }
    /**
     * Allows the owner to add validator
     * @param _validator New validator address
     */

    function addValidator(address _validator) public  {
        // require(msg.sender == owner, "Only owner can add validators");
        validators.push(_validator);
        validatorSetMap[_validator].active = true;
        currentValidatorSetMap[_validator] = index;
        index++;
        emit AddValidator();

    }

    /**
     * Allows the owner to remove validator by index
     * @param _index validator index
     */
    function removeValidatorByIndex(uint256 _index) external  {
        // require(msg.sender == owner, "Only owner can remove validators");
        require(_index < validators.length, "Index out of bounds");
        require(validators.length > 1, "Last validator can't be remove");
        validators[_index] = validators[validators.length - 1];
        validators.pop();
        delete validatorSetMap[validators[_index]]; 
        // validatorSetMap[validators[_index]].active = true;
        emit RemoveValidator();
    }

    /**
     * Get All Validators Address 
     */
    function getValidators() public view returns (address[] memory) {
        return validators;
    }

    /**
     * Get All Validators Address 
     */
    function isValidatorAddress(address _validator) public view returns (bool) {
        // require(validatorSetMap[_validator].jailed == false || validatorSetMap[_validator].jailed == true, "Only owner can remove validators");
        return validatorSetMap[_validator].active;
    }

    /*********************** External Functions **************************/
  function deposit(address valAddr) external payable noEmptyDeposit{
    uint256 value = msg.value;
    uint256 _index = currentValidatorSetMap[valAddr];

    // uint256 curBurnRatio = INIT_BURN_RATIO;
    // if (burnRatioInitialized) {
    //   curBurnRatio = burnRatio;
    // }

    // if (value > 0 && curBurnRatio > 0) {
    //   uint256 toBurn = value.mul(curBurnRatio).div(BURN_RATIO_SCALE);
    //   if (toBurn > 0) {
    //     address(uint160(BURN_ADDRESS)).transfer(toBurn);
    //     emit feeBurned(toBurn);

    //     value = value.sub(toBurn);
    //   }
    // }

    if (_index>0) {
      Validator storage validator = validatorSetMap[validators[_index-1]];
    //   if (validator.jailed) {
    //     // emit deprecatedDeposit(valAddr,value);
    //   } else {
        totalInComing = totalInComing + value;
        validator.incoming = validator.incoming + value;
        emit validatorDeposit(valAddr,value);
      }
    // } else {
      // get incoming from deprecated validator;
    //   emit deprecatedDeposit(valAddr,value);
    }
  }
// }
// }