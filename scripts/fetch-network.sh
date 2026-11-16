#!/bin/sh
# downloads the open network data the gateway classifies client networks
# with, for one month, checks it with the gateway's own loader and writes the
# attribution next to it. every source is pinned by the month, so two builds
# of the same month carry the same data:
#
#   db-ip ip to asn lite, <month>          cc by 4.0, https://db-ip.com
#   ipverse as-metadata, the last commit    cc0 1.0, https://github.com/ipverse/as-metadata
#     on the first day of <month>
#   tor exit addresses of the month         cc0, tor project collector
#     before <month>                          https://collector.torproject.org
#
# usage: scripts/fetch-network.sh <output dir> [YYYY-MM]
# the month defaults to the current utc month. nothing falls back on its own:
# a missing file stops the script.
#
# outputs: <dir>/dbip-asn-lite.mmdb, <dir>/asn-classes.csv,
# <dir>/tor-exits.txt, <dir>/network-data.month, <dir>/NETWORK-ATTRIBUTION.txt
set -eu

if [ $# -lt 1 ] || [ $# -gt 2 ]; then
	echo "usage: $0 <output dir> [YYYY-MM]" >&2
	exit 2
fi
out=$1
month=${2:-$(date -u +%Y-%m)}
case $month in
[0-9][0-9][0-9][0-9]-[0-1][0-9]) ;;
*)
	echo "fetch-network: month '$month' is not YYYY-MM" >&2
	exit 2
	;;
esac

# the tor archive of the month before: the newest complete month
year=${month%-*}
mon=$(expr "${month#*-}" + 0)
if [ "$mon" -eq 1 ]; then
	prev="$(expr "$year" - 1)-12"
else
	prev=$(printf '%s-%02d' "$year" "$(expr "$mon" - 1)")
fi

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

asn_url="https://download.db-ip.com/free/dbip-asn-lite-${month}.mmdb.gz"
echo "fetch-network: downloading $asn_url"
curl -fsSL --retry 3 --max-time 300 -o "$tmp/asn.mmdb.gz" "$asn_url"
gzip -dc "$tmp/asn.mmdb.gz" >"$tmp/dbip-asn-lite.mmdb"

# the as-metadata commit current on the first day of the month. the api
# answers the newest commit at or before "until"
until="${month}-01T23:59:59Z"
commits_url="https://api.github.com/repos/ipverse/as-metadata/commits?until=${until}&per_page=1"
curl -fsSL --retry 3 --max-time 60 -H 'Accept: application/vnd.github+json' -o "$tmp/commits.json" "$commits_url"
sha=$(sed -n 's/^ *"sha": *"\([0-9a-f]\{40\}\)".*/\1/p' "$tmp/commits.json" | head -n 1)
if [ -z "$sha" ]; then
	echo "fetch-network: no as-metadata commit at or before $until" >&2
	exit 1
fi
meta_url="https://raw.githubusercontent.com/ipverse/as-metadata/${sha}/as.json"
echo "fetch-network: downloading $meta_url"
curl -fsSL --retry 3 --max-time 600 -o "$tmp/as.json" "$meta_url"
(cd "$root" && go run ./scripts/netdata classes "$tmp/as.json" "$tmp")

tor_url="https://collector.torproject.org/archive/exit-lists/exit-list-${prev}.tar.xz"
echo "fetch-network: downloading $tor_url"
curl -fsSL --retry 3 --max-time 600 -o "$tmp/tor.tar.xz" "$tor_url"
# every address that exited tor during the month, one per line
tar -xJOf "$tmp/tor.tar.xz" | awk '$1 == "ExitAddress" { print $2 }' | sort -u >"$tmp/tor-exits.txt"

# the go checker opens the three files with the gateway's own loader
(cd "$root" && go run ./scripts/netdata check "$tmp")

mv "$tmp/dbip-asn-lite.mmdb" "$tmp/asn-classes.csv" "$tmp/tor-exits.txt" "$out/"
printf '%s\n' "$month" >"$out/network-data.month"
cat >"$out/NETWORK-ATTRIBUTION.txt" <<EOF
IP to ASN data by DB-IP (https://db-ip.com)

File: dbip-asn-lite-${month}.mmdb, from ${asn_url}
The DB-IP IP to ASN Lite database is licensed under the Creative Commons
Attribution 4.0 International License (CC BY 4.0):
https://creativecommons.org/licenses/by/4.0/
The file is used unmodified. The gateway looks up only the /24 (IPv4) or /48
(IPv6) network of a client address.

AS categories by ipverse as-metadata (https://github.com/ipverse/as-metadata)
Commit ${sha}, from ${meta_url}
Released under CC0 1.0 Universal. asn-classes.csv is derived from the
category and networkRole fields of as.json.

Tor exit addresses by the Tor Project (https://metrics.torproject.org)
File: exit-list-${prev}.tar.xz, from ${tor_url}
Tor Metrics data is released under a CC0 no copyright declaration.
tor-exits.txt holds every ExitAddress of the month.
EOF
echo "fetch-network: wrote $out ($month, as-metadata $sha, tor exits of $prev)"
