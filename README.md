docker-iqfeed
===============
Access IQFeed datafeed through TCP (port 9100) or HTTP (port 8080);

This is an Alpine-docker container that runs wine/Xvfb/IQFeed and offer this as TCP/HTTP-endpoint.

Usage container
=========
Copy the `iqfeed.env.example` to `iqfeed.env` and configure the variables to match as supplied by IQFeed.
https://github.com/mpdroog/docker-iqfeed/blob/main/iqfeed.env.example

Next pull this project from hub.docker
```bash
docker pull mpdroog/docker-iqfeed:v1
```

OR build it yourself with `build.sh`
```bash
#!/bin/bash
set -euo pipefail
IFS=$'\n\t'
IQFEED_INSTALLER_BIN="iqfeed_client_6_2_0_25.exe"

# Download IQFeed binary (so we only download it once)
mkdir cache
wget -nv http://www.iqfeed.net/$IQFEED_INSTALLER_BIN -O ./cache/$IQFEED_INSTALLER_BIN

# Build the API-tool
cd uptool
env GOOS=linux GOARCH=amd64 go build
cd -

# Build the container
docker build --tag 'mpdroog/docker-iqfeed:latest' -f Dockerfile .
# Run it
docker run -p 9100:9101 -p 8080:8080 -p 5009:5010 -p 9200:9201 --cap-drop ALL --security-opt no-new-privileges --memory=1g --cpus=1 --rm --env-file iqfeed.env mpdroog/docker-iqfeed
```

Ports
=========
This daemon offers the 'classic' TCP connection (9100) and HTTP-REST (8080) for getting the ticker data out.
Additionally, Level 1 (streaming quotes) and Level 2 (market depth) are available as TCP proxies.

```
External -> Internal
9100 -> 9101  LookupPort - Historical Data, Symbol Lookup, News Lookup, and Chains Lookup
8080 -> 8080  HTTP - Historical Data, Symbol Lookup
5009 -> 5010  Level1 - Streaming Level 1 quotes (watch symbols, get real-time updates)
9200 -> 9201  Level2 - Market Depth (order book, price levels)
```

HTTP example
=========
The datapoints through HTTP are limited to `MaxDatapoints(10.000)`. If you want a higher amount use the second example that reads minute-data using `mode=chunked` with CSV.

```bash
# Get 1 daily candle for MicroStrategy as JSON
$ curl "http://localhost:8080/ohlc?asset=MSTR&range=DAILY&datapoints=1"
[
  {
    "Close": "111.1100",
    "Datetime": "2023-05-26",
    "High": "111.1000",
    "Low": "111.1000",
    "Open": "111.1000"
  }
]

# Get ALL minute candles for Apple (since inception) and stream as CSV
$ curl --header "Accept: text/csv" "http://localhost:8080/ohlc-intervals?asset=AAPL&mode=chunked&interval=60&datapoints=0"
MessageID, TimeStamp, High, Low, Open, Close, TotalVolume, PeriodVolume, NumberofTrades,
LH,2024-05-10 05:32:00,184.7600,184.7500,184.7500,184.7600,32048,250,0,
....
```

For all accepted HTTP-endpoints there is a human-readable overview on http://localhost:8080

TCP example
=========
```bash
$ telnet 0 9100
Trying 0.0.0.0...
Connected to 0.
Escape character is '^]'.
S,SET PROTOCOL,6.2
S,CURRENT PROTOCOL,6.2
HDX,MSTR,1
LH,2023-05-26,111.1100,111.1000,111.1000,111.1000,111111,0,
!ENDMSG!,
quit
Connection closed by foreign host.
```

For all accepted commands by IQFeed have a look at http://www.iqfeed.net/dev/api/docs/HistoricalviaTCPIP.cfm

Level 1 example
=========
```bash
$ telnet 0 5009
Trying 0.0.0.0...
Connected to 0.
Escape character is '^]'.
S,KEY,xxxxx
S,SERVER CONNECTED
S,CUST,...
S,SET PROTOCOL,6.2
S,CURRENT PROTOCOL,6.2
wAAPL
F,AAPL,...  (fundamental data)
P,AAPL,...  (summary/snapshot)
Q,AAPL,...  (streaming updates)
Q,AAPL,...
rAAPL
quit
```

Commands: `w[SYMBOL]` watch, `r[SYMBOL]` unwatch, `S,SET PROTOCOL,6.2` set protocol.
For all Level 1 commands see http://www.iqfeed.net/dev/api/docs/Level1viaTCPIP.cfm

Level 2 example
=========
```bash
$ telnet 0 9200
Trying 0.0.0.0...
Connected to 0.
Escape character is '^]'.
S,SERVER CONNECTED
S,SET PROTOCOL,6.2
S,CURRENT PROTOCOL,6.2
WPL,@ESM25,10
7,@ESM25,B,5000.00,150,5,2,...  (price level summary - bid)
7,@ESM25,A,5001.00,120,3,2,...  (price level summary - ask)
8,@ESM25,B,5000.00,155,6,2,...  (price level update)
RPL,@ESM25
quit
```

Commands: `WPL,[SYMBOL],[MaxLevels]` watch price levels, `RPL,[SYMBOL]` unwatch, `WOR,[SYMBOL]` watch orders.
For all Level 2 commands see http://www.iqfeed.net/dev/api/docs/MarketDepth.cfm

Logic
=========
The iqapi-tool is a combination of services allowing us to build layer on layer with precise control.

```
Layer1. Start xvfb
Layer2. Start wine64 iqfeed
Layer3. Start and listen on admin-conn (9300)
Layer4. Start and listen for your reqs (9100)
```

(i) When xvfb crashes it will respawn xvfb and only respawn iqfeed when xvfb is running again.

Logs?
=========
iqapi is a blocking-process that writes everything to stdout+stderr so Docker should offer all
 of that to your standard logging facility (probably journal on systemd env).

There is an IQFeed errorlog available in the container:
```bash
docker ps
CONTAINER ID   IMAGE           COMMAND               CREATED          STATUS          PORTS                                            NAMES
.....

docker exec -it PUT_CONTAINER_ID_HERE /bin/sh
# Print current instance log
cat /home/wine/.wine/drive_c/users/wine/Documents/DTN/IQFeed/IQConnectLog.txt
# Print log of instance before it respawned
cat /home/wine/IQConnectLog.crash.txt
```

Errors for TCP-socket?
=========
```
E,NO_DAEMON = iqfeed.exe not running (yet)
E,NO_ADMIN = admin(port 9300) did not give a Connected-status (yet)
E,PARSE_DURATION = failed parsing time.ParseDuration, this is a dev bug!
E,CONN_SET_DEADLINE = failed configuring deadline on client socket
E,UPSTREAM CONN_TIMEOUT = failed connecting to iqfeed (socket 9100)
E,UPSTREAM_T = failed writing TEST-cmd to upstream to check if connection is ready
E,UPSTREAM_T_RES = failed reading upstream TEST-cmd reply
E,UPSTREAM_T_INV = invalid upstream response for TEST-cmd
E,UPSTREAM SET_DEADLINE = failed configuring deadline on upstream socket
E,CONN_READ_CMD = failed reading client command
E,UPSTREAM_W = failed writing client command to upstream
E,UPSTREAM_R = failed reading reply from upstream
```

Credits
=========
This project was built using:
https://github.com/jaikumarm/docker-iqfeed
