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

User privilege
=========
Dockerfile instructs

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