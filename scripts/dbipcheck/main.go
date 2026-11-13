// Command dbipcheck opens a db-ip country lite file with the gateway's own
// reader, so fetch-dbip.sh accepts exactly the files the gateway will load.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/justmert/veristar/internal/gateway"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dbipcheck <file.mmdb>")
		os.Exit(2)
	}
	built, err := check(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("dbipcheck: ok, built %s\n", built.Format(time.RFC3339))
}

func check(path string) (time.Time, error) {
	g, err := gateway.OpenGeo(path)
	if err != nil {
		return time.Time{}, err
	}
	defer g.Close()
	// a public resolver address with a long-stable allocation must resolve to
	// some country, otherwise the file opens but holds no usable records
	ip, err := gateway.ParseClientIP("1.1.1.1")
	if err != nil {
		return time.Time{}, err
	}
	cc, err := g.Country(ip)
	if err != nil {
		return time.Time{}, err
	}
	if cc == "" {
		return time.Time{}, errors.New("dbipcheck: no country for a public address")
	}
	return g.BuildTime(), nil
}
