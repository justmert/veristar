package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadStellar(t *testing.T) {
	c, err := LoadStellar(FromMap(nil))
	require.NoError(t, err)
	require.Equal(t, Stellar{HorizonURL: DefaultHorizonURL, RPCURL: DefaultRPCURL, HorizonRPS: DefaultHorizonRPS, RPCRPS: DefaultRPCRPS}, c)

	c, err = LoadStellar(FromMap(map[string]string{
		"STELLAR_HORIZON_URL": "https://horizon.example.org", "STELLAR_RPC_URL": "https://rpc.example.org/key",
		"STELLAR_HORIZON_RPS": "0.5", "STELLAR_RPC_RPS": "10",
	}))
	require.NoError(t, err)
	require.Equal(t, Stellar{HorizonURL: "https://horizon.example.org", RPCURL: "https://rpc.example.org/key", HorizonRPS: 0.5, RPCRPS: 10}, c)

	_, err = LoadStellar(FromMap(map[string]string{"STELLAR_HORIZON_URL": "ftp://x", "STELLAR_RPC_RPS": "0"}))
	require.ErrorContains(t, err, "STELLAR_HORIZON_URL")
	require.ErrorContains(t, err, "STELLAR_RPC_RPS")
}
