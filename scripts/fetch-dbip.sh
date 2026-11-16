#!/bin/sh
# downloads the db-ip ip to country lite database for one month, checks that
# it decompresses and opens as a db-ip country file, and writes the cc by 4.0
# attribution next to it.
#
# usage: scripts/fetch-dbip.sh <output dir> [YYYY-MM]
# the month defaults to the current utc month. db-ip publishes the new file
# on the first of the month, so a run early on the first can fail with 404;
# pass the previous month explicitly then. nothing falls back on its own.
#
# outputs: <dir>/dbip-country-lite.mmdb, <dir>/dbip-country-lite.mmdb.month,
# <dir>/DBIP-ATTRIBUTION.txt
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
	echo "fetch-dbip: month '$month' is not YYYY-MM" >&2
	exit 2
	;;
esac

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
mkdir -p "$out"
out=$(cd "$out" && pwd)

url="https://download.db-ip.com/free/dbip-country-lite-${month}.mmdb.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "fetch-dbip: downloading $url"
# db-ip answers some downloads from shared addresses (ci runners) with 403
# and serves the same file a little later, so every error is retried
curl -fsSL --retry 5 --retry-all-errors --retry-delay 20 --max-time 300 -o "$tmp/db.mmdb.gz" "$url"
gzip -dc "$tmp/db.mmdb.gz" >"$tmp/db.mmdb"

# the go checker opens the file with the same reader the gateway uses and
# refuses anything that is not a db-ip country file
(cd "$root" && go run ./scripts/dbipcheck "$tmp/db.mmdb")

mv "$tmp/db.mmdb" "$out/dbip-country-lite.mmdb"
printf '%s\n' "$month" >"$out/dbip-country-lite.mmdb.month"
cat >"$out/DBIP-ATTRIBUTION.txt" <<EOF
IP Geolocation by DB-IP (https://db-ip.com)

File: dbip-country-lite-${month}.mmdb, from ${url}
The DB-IP IP to Country Lite database is licensed under the Creative Commons
Attribution 4.0 International License (CC BY 4.0):
https://creativecommons.org/licenses/by/4.0/

The file is used unmodified. The gateway looks up only the /24 (IPv4) or /48
(IPv6) network of a client address and keeps only the ISO 3166-1 alpha-2
country code.
EOF
echo "fetch-dbip: wrote $out/dbip-country-lite.mmdb ($month)"
