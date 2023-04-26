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

    mapping(uint256 => CValidator[]) public cValidators;
    mapping(uint256 => CValidator[]) public cProducers;

    mapping (uint256 => Epoch) public epoch; // span number => span
    uint256[] public spanNumbers; // recent span numbers


    uint256 public constant FIRST_END_BLOCK = 30;

    struct CValidator{
        // only in state
        // uint64  votingPower;
        uint256 id;
        // bool active;
        uint256 power;
        // uint256 incoming;
    }

    struct Validator{
        // only in state
        // uint64  votingPower;
        // uint256 id;
        bool active;
        // uint256 power;
        uint256 incoming;
    }

    // span details
    struct Epoch {
      uint256 number;
      uint256 startBlock;
      uint256 endBlock;
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


    function setInitialValidators() public {

      // initial span
      uint256 span = 0;
      epoch[span] = Epoch({
        number: span,
        startBlock: 50,
        endBlock: 100
      });
      spanNumbers.push(span);
      // cValidators[span].length = 0;
      // cProducers[span].length = 0;

      for (uint256 i = 0; i < 500; i++) {
        // cValidators[span].length++;
        cValidators[span][i] = CValidator({
          id: i,
          power: (i%1000)+50
          // signer: d[i]
        });
      }

      for (uint256 i = 0; i < 500; i++) {
        // cProducers[span].length++;
        cProducers[span][i] = CValidator({
          id: i,
          power: (i%1000)+50
          // signer: d[i]
        });
      }
    }


    function commitSpan(
      uint256 newSpan,
      uint256 startBlock,
      uint256 endBlock,
      bytes calldata validatorBytes,
      bytes calldata producerBytes
    ) external {
      // current span
      uint256 span = currentSpanNumber();
      // set initial validators if current span is zero
    if (span == 0) {
      setInitialValidators();
    }

    // check conditions
    require(newSpan == span + 1, "Invalid span id");
    require(endBlock > startBlock, "End block must be greater than start block");
    // require((endBlock - startBlock + 1) % SPRINT == 0, "Difference between start and end block must be in multiples of sprint");
    require(epoch[span].startBlock <= startBlock, "Start block must be greater than current span");

    // check if already in the span
    require(epoch[newSpan].number == 0, "Span already exists");

    // store span
    // epoch[newSpan] = Epoch({
    //   number: newSpan,
    //   startBlock: startBlock,
    //   endBlock: endBlock
    // });
    // spanNumbers.push(newSpan);
    // validators[newSpan].length = 0;
    // producers[newSpan].length = 0;

    // set validators
    // RLPReader.RLPItem[] memory validatorItems = validatorBytes.toRlpItem().toList();
    // for (uint256 i = 0; i < validatorItems.length; i++) {
    //   RLPReader.RLPItem[] memory v = validatorItems[i].toList();
    //   validators[newSpan].length++;
    //   validators[newSpan][i] = Validator({
    //     id: v[0].toUint(),
    //     power: v[1].toUint(),
    //     signer: v[2].toAddress()
    //   });
    // }

    // set producers
    // RLPReader.RLPItem[] memory producerItems = producerBytes.toRlpItem().toList();
    // for (uint256 i = 0; i < producerItems.length; i++) {
    //   RLPReader.RLPItem[] memory v = producerItems[i].toList();
    //     producers[newSpan].length++;
    //     producers[newSpan][i] = Validator({
    //       id: v[0].toUint(),
    //       power: v[1].toUint(),
    //       signer: v[2].toAddress()
    //     });
    // }
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

    function currentSpanNumber() public view returns (uint256) {
      return getSpanByBlock(block.number);
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


    // get bor validator
    function getBKCValidators(uint256 number) public view returns (address[] memory, uint256[] memory) {
      // if (number <= FIRST_END_BLOCK) {
      //   return getInitialValidators();
      // }

      // span number by block
      uint256 span = getSpanByBlock(number);

      address[] memory addrs = new address[](cProducers[span].length);
      uint256[] memory powers = new uint256[](cProducers[span].length);
      for (uint256 i = 0; i < cProducers[span].length; i++) {
        // addrs[i] = cProducers[span][i].signer;
        powers[i] = cProducers[span][i].power;
      }

      return (addrs, powers);
    }

    // get span number by block
    function getSpanByBlock(uint256 number) public view returns (uint256) {
      for (uint256 i = spanNumbers.length; i > 0; i--) {
        Epoch memory span = epoch[spanNumbers[i - 1]];
        if (span.startBlock <= number && span.endBlock != 0 && number <= span.endBlock) {
          return span.number;
        }
      }

      // if cannot find matching span, return latest span
      if (spanNumbers.length > 0) {
        return spanNumbers[spanNumbers.length - 1];
      }

      // return default if not found any thing
      return 0;
  }
}
// }
// }