// Package gatewayimage holds the docker build of the gateway image that the
// stack tests run (deploy/images/gateway.Dockerfile). only *_test.go files
// import it.
package gatewayimage

import "os"

// BuildArgs returns the docker arguments that build the gateway image for the
// db-ip month as tag, from the repository root. when GATEWAY_GEO_DIR names a
// directory written by make geo-fetch, the build takes the month's files from
// it (the build context named geo) instead of downloading them again; the
// image build checks their month and opens them with the gateway's loaders
// either way. ci sets it, because db-ip refuses some downloads from shared
// runner addresses.
func BuildArgs(month, tag string) []string {
	args := []string{"build", "-q", "-f", "deploy/images/gateway.Dockerfile", "--build-arg", "DBIP_MONTH=" + month}
	if dir := os.Getenv("GATEWAY_GEO_DIR"); dir != "" {
		args = append(args, "--build-context", "geo="+dir)
	}
	return append(args, "-t", tag, ".")
}
