docker-iqfeed
===============
Access IQFeed datafeed through TCP (port 9100) or HTTP (port 8080);

This is a Ubuntu-docker container that runs wine/Xvfb/IQFeed and offer this as TCP/HTTP-endpoint.

Usage container
=========
Copy the `iqfeed.env.example` to `iqfeed.env` and configure the variables to match as supplied by IQFeed.
https://github.com/mpdroog/docker-iqfeed/blob/main/iqfeed.env.example

Next pull this project from hub.docker
```bash
docker pull mpdroog/docker-iqfeed
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

# Build the API-tool (you need Golang for this)
cd uptool
env GOOS=linux GOARCH=amd64 go build
cd -

# Build the container
docker build --tag 'docker-iqfeed' .
# Run it
docker run -p 9100:9101 -p 8080:8080 --cap-drop ALL --security-opt no-new-privileges --memory=256m --cpus=1 --rm --env-file iqfeed.env docker-iqfeed
```

Ports
=========
This daemon offers the 'classic' TCP connection (9100) and HTTP (8080) for getting the ticker data out.

```
LookupPort 9100 - Historical Data, Symbol Lookup, News Lookup, and Chains Lookup information
HTTP 8080 - Historical Data
```

HTTP example
=========
```bash
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
```

For all accepted HTTP-endpoints there is a human-readable overview on http://localhost:8080

TCP example
=========
```bash
$ telnet 0 9100
Trying 0.0.0.0...
Connected to 0.
Escape character is '^]'.
READY
S,SET PROTOCOL,6.2
S,CURRENT PROTOCOL,6.2
HDX,MSTR,1
LH,2023-05-26,111.1100,111.1000,111.1000,111.1000,111111,0,
!ENDMSG!,
quit
Connection closed by foreign host.
```

For all accepted commands by IQFeed have a look at http://www.iqfeed.net/dev/api/docs/HistoricalviaTCPIP.cfm

Difference with regular port 9100
=========
The TCP-server in this Docker container proxy's any commands to IQFeed (upstream). Because of this I've
adjusted the code to send an `READY\r\n` from the server instead of waiting for the client to initiate the connection.
(Motivation is that this way you can see why it failed)

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
cat /home/wine/DTN/IQFeed/IQConnectLog.txt
# Print log of instance before it respawned
cat /home/wine/DTN/IQFeed/IQConnectLog.txt.1
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
