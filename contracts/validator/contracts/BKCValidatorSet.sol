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
    
    address[] public validators;
    address public owner;

    /**********
     * Events *
     **********/
    event AddValidator(address _validator);
    event RemoveValidator(address _validator);

    /***************
     * Constructor *
     ***************/

    constructor() {
        owner = msg.sender;
        init();
    }

    function init() public {
        require(msg.sender == owner, "Only owner can initialize the contract");
        validators.push(0x8A9a4147A0c3c4ff45c2DC14a3aC94D1c899E0Cf);
    }
    /**
     * Allows the owner to add validator
     * @param _validator New validator address
     */

    function addValidator(address _validator) external  {
        require(msg.sender == owner, "Only owner can add validators");
        validators.push(_validator);
        emit AddValidator(_validator);

    }

    /**
     * Allows the owner to remove validator
     * @param _index validator index
     */
    function removeValidatorByIndex(uint256 _index) external  {
        require(msg.sender == owner, "Only owner can remove validators");
        require(_index < validators.length, "Index out of bounds");
        delete validators[_index];
        emit RemoveValidator(validators[_index]);
    }

    function getValidators() public view returns (address[] memory) {
        return validators;
    }
}


