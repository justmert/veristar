package config

import (
	"fmt"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stretchr/testify/require"
)

func TestLoadReportSigning(t *testing.T) {
	kp, other := keypair.MustRandom(), keypair.MustRandom()
	c, err := LoadReportSigning(FromMap(map[string]string{
		"REPORT_SIGNING_SEED": kp.Seed(), "REPORT_SIGNING_KEY": kp.Address(),
		"REPORT_SIGNING_RETIRED_KEYS": " " + other.Address() + " ",
	}))
	require.NoError(t, err)
	require.Equal(t, kp.Address(), c.Key.Full().Address())
	require.Equal(t, []string{other.Address()}, c.Retired)
	// the seed never prints
	require.NotContains(t, fmt.Sprintf("%v %+v %#v %s", c, c, c, c.Key), kp.Seed())

	_, err = LoadReportSigning(FromMap(map[string]string{"REPORT_SIGNING_SEED": kp.Seed(), "REPORT_SIGNING_KEY": other.Address()}))
	require.ErrorContains(t, err, "is not the address of REPORT_SIGNING_SEED")
	require.NotContains(t, err.Error(), kp.Seed())
	_, err = LoadReportSigning(FromMap(map[string]string{"REPORT_SIGNING_SEED": "SNOTASEED", "REPORT_SIGNING_KEY": "GX"}))
	require.ErrorContains(t, err, "REPORT_SIGNING_SEED")
	require.ErrorContains(t, err, "REPORT_SIGNING_KEY")
	_, err = LoadReportSigning(FromMap(nil))
	require.ErrorContains(t, err, "REPORT_SIGNING_SEED is required")
	_, err = LoadReportSigning(FromMap(map[string]string{
		"REPORT_SIGNING_SEED": kp.Seed(), "REPORT_SIGNING_KEY": kp.Address(), "REPORT_SIGNING_RETIRED_KEYS": kp.Address(),
	}))
	require.ErrorContains(t, err, "REPORT_SIGNING_RETIRED_KEYS")
}
