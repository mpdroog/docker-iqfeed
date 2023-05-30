<?php
/**
 * Health check for iqfeed-container
 */
declare(strict_types=1);
define("VERBOSE", isset(getopt("v::")["v"]));

function err($msg)
{
        if (VERBOSE) echo $msg . "\n";
        exit(1);
}

$conn = @fsockopen("127.0.0.1", 9100, $errno, $errstr, 2);
{
  if ($conn === false) {
        err("CRITICAL - stream_socket_client fail: $errno $errstr");
  }
  if (stream_set_timeout($conn, 30) === false) {
        err("UNKNOWN - stream_set_timeout fail");
  }
  $res = stream_get_line($conn, 256, "\r\n");
  if ($res !== "READY") {
        err("UNKNOWN - set protocol failed, res=$res");
  }
}

if (VERBOSE) echo "OK\n";
