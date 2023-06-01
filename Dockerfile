# Build using Alpine +- 900MB
FROM alpine:latest
MAINTAINER mpdroog "github.com/mpdroog/docker-iqfeed"
LABEL description="IQFeed Historical data TCP+HTTP API"

# Creating the wine user and setting up dedicated non-root environment
RUN adduser -D wine
ENV HOME /home/wine
WORKDIR /home/wine

ENV WINEPREFIX /home/wine/.wine
ENV DISPLAY :0

# iqfeed config
ENV IQFEED_INSTALLER_BIN="iqfeed_client_6_2_0_25.exe"
ENV IQFEED_LOG_LEVEL 0xB222

# Hide all warning
ENV WINEDEBUG -all

RUN ["apk", "--no-cache", "--allow-untrusted", "-v", "add", "wine", "xvfb", "xvfb-run"]
RUN wget http://winetricks.org/winetricks && chmod +x winetricks && mv winetricks /usr/bin/winetricks

USER wine
# Init wine instance
RUN winecfg && wineserver --wait
# Download iqfeed client
#RUN wget -nv http://www.iqfeed.net/$IQFEED_INSTALLER_BIN -O /home/wine/$IQFEED_INSTALLER_BIN
ADD cache/$IQFEED_INSTALLER_BIN /home/wine/$IQFEED_INSTALLER_BIN

# Install iqfeed client
RUN xvfb-run -s -noreset -a wine64 /home/wine/$IQFEED_INSTALLER_BIN /S && wineserver --wait
RUN wine64 reg add HKEY_CURRENT_USER\\\Software\\\DTN\\\IQFeed\\\Startup /t REG_DWORD /v LogLevel /d $IQFEED_LOG_LEVEL /f && wineserver --wait
ADD uptool/iqapi /home/wine/iq-api

# Correct X-perm warn
USER root
RUN chown root:root /tmp/.X11-unix
USER wine

EXPOSE 9101

CMD ["/home/wine/iq-api"]
