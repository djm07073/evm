package integration

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/evm/tests/integration/x/vm"
	utiltx "github.com/cosmos/evm/testutil/tx"
	evmtypes "github.com/cosmos/evm/x/vm/types"
)

// BenchmarkApplyTransaction runs the ApplyTransaction benchmark
func BenchmarkApplyTransaction(b *testing.B) {
	suite := vm.NewKeeperTestSuite(CreateEvmd)
	suite.EnableFeemarket = false
	suite.EnableLondonHF = true
	suite.SetT(&testing.T{})
	suite.SetupTest()

	ethSigner := ethtypes.LatestSignerForChainID(evmtypes.GetEthChainConfig().ChainID)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		addr := suite.Keyring.GetAddr(0)
		krSigner := utiltx.NewSigner(suite.Keyring.GetPrivKey(0))
		
		// Create access list transaction
		templateAccessListTx := &ethtypes.AccessListTx{
			GasPrice: big.NewInt(1),
			Gas:      21000,
			To:       &common.Address{},
			Value:    big.NewInt(0),
			Data:     []byte{},
			Nonce:    suite.Network.App.GetEVMKeeper().GetNonce(suite.Network.GetContext(), addr),
		}
		
		ethTx := ethtypes.NewTx(templateAccessListTx)
		msg := &evmtypes.MsgEthereumTx{}
		err := msg.FromEthereumTx(ethTx)
		require.NoError(b, err)
		msg.From = addr.Bytes()
		err = msg.Sign(ethSigner, krSigner)
		require.NoError(b, err)

		b.StartTimer()
		resp, err := suite.Network.App.GetEVMKeeper().ApplyTransaction(suite.Network.GetContext(), msg)
		b.StopTimer()

		require.NoError(b, err)
		require.False(b, resp.Failed())
	}
}