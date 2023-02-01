// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.0;

contract BKCValidatorSet {
    /*************
     * Variables *
     *************/

    // struct Validator{
    //     address consensusAddress;
    //     address payable feeAddress;
    //     address BBCFeeAddress;
    //     uint64  votingPower;

    //     // only in state
    //     bool jailed;
    //     uint256 incoming;
    // }
    
    mapping(address => Validator) public validatorSetMap;
    address[] public validators;
    address public owner;

    struct Validator{
        // only in state
        // uint64  votingPower;
        bool active;
    }

    /**********
     * Events *
     **********/
    event AddValidator();
    event RemoveValidator();

    /***************
     * Constructor *
     ***************/

    constructor() {
        owner = msg.sender;
    }

    function init() public {
        require(msg.sender == owner, "Only owner can initialize the contract");
        // first validator set
        validators.push(0x065Cac36eaa04041D88704241933c41aabFe83eE);
        validators.push(0xb5B6221fA2d05174a4deDB8d700Ccc5446032176);
        // validators.push(0x96C9F2F893AdeF66669B4bB4A7dfA5006c037CD3);
    }
    /**
     * Allows the owner to add validator
     * @param _validator New validator address
     */

    function addValidator(address _validator) external  {
        require(msg.sender == owner, "Only owner can add validators");
        validators.push(_validator);
        validatorSetMap[_validator].active = true;
        emit AddValidator();

    }

    /**
     * Allows the owner to remove validator by index
     * @param _index validator index
     */
    function removeValidatorByIndex(uint256 _index) external  {
        require(msg.sender == owner, "Only owner can remove validators");
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
}