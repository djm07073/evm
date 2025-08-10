package types

// Evm module events
const (
	EventTypeEthereumTx = TypeMsgEthereumTx
	EventTypeBlockBloom = "block_bloom" // DEPRECATED: Using FilterMaps instead
	EventTypeTxLog      = "tx_log"
	EventTypeFeeMarket  = "evm_fee_market"

	AttributeKeyBaseFee         = "base_fee"
	AttributeKeyContractAddress = "contract"
	AttributeKeyRecipient       = "recipient"
	AttributeKeyTxHash          = "txHash"
	AttributeKeyEthereumTxHash  = "ethereumTxHash"
	AttributeKeyTxIndex         = "txIndex"
	AttributeKeyTxGasUsed       = "txGasUsed"
	AttributeKeyTxType          = "txType"
	AttributeKeyTxLog           = "txLog"

	// tx failed in eth vm execution
	AttributeKeyEthereumTxFailed = "ethereumTxFailed"
	AttributeValueCategory       = ModuleName
	AttributeKeyEthereumBloom    = "bloom" // DEPRECATED: Using FilterMaps instead

	MetricKeyTransitionDB = "transition_db"
	MetricKeyStaticCall   = "static_call"
)
