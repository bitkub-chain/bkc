package clique

import (
	"context"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

//go:generate mockgen -destination=./mock/ethapi_mock.go -package=mock . EthAPI
type EthAPI interface {
	GetHeaderTypeByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Header, error)
}
