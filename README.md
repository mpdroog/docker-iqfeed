docker-iqfeed
===============
A Ubuntu-docker container with Wine/IQFeed to access the IQFeed-API
with it's high quality OHLC-data.

A small self-written tool (uptool/iqapi) is used to keep all processes
 inside this container running (think supervisor) and prepare sockets
 for optimal usage (trying to prevent timeouts in your tools)

In this rootfolder is a file (build.sh) that builds a Docker container and
spawns an instance once it's been built.

Ports
=========
This daemon only exposes LookupPort(9100 => Historical Data, Symbol Lookup, News Lookup, and Chains Lookup information)
It has a simple TCP-daemon implemented with only Historical Data feeds tested.

```
LookupPort 9101
```

The available ports are +1 compared to IQFeed, we do this as we proxy between 127.0.0.1:9100 and 0:9101;
The proxy is there to be sure the connection works before offering it (solving timeouts for the container user)

http://www.iqfeed.net/dev/api/docs//Introduction.cfm (Socket Connections)

Difference with regular port 9100
=========
Instead of waiting for the client to send the first message the server initiated it by
either directly sending an error (i.e. `E,NO_ADMIN\r\n"`) or sending `READY\r\n`

User privilege
=========
All is ran as user wine (uid 1001)

Logic
=========
The iqapi-tool is a combination of services allowing us to build layer on layer with precise control.

Layer1. Start xvfb
Layer2. Start wine64 iqfeed
Layer3. Start and listen on admin-conn (9300)
Layer4. Start and listen for your reqs (9101)

(i) When xvfb crashes it will respawn xvfb and only respawn iqfeed when xvfb is running again.

Logs?
=========
iqapi is a blocking-process that writes everything to stdout+stderr so Docker should offer all
 of that to your standard logging facility (probably journal on systemd env).
 
This project was built using:
https://github.com/jaikumarm/docker-iqfeed

Errors?
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