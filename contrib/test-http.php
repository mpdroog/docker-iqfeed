<?php
/**
 * Test does a simple health check to see if
 * the conn to IQFeed is still alive or needs some kicking
 */
declare(strict_types=1);
define("VERBOSE", isset(getopt("v::")["v"]));

function err($msg)
{
	if (VERBOSE) echo $msg . "\n";
	exit(1);
}

// Health check for iqfeed
$url = "http://127.0.0.1:8080/search?field=SYMBOL&search=GOOG&type=EQUITY";
$ok = 1;

$ch = curl_init($url);
$ok &= curl_setopt_array($ch, [
	CURLOPT_TIMEOUT => 10,
	CURLOPT_CONNECTTIMEOUT => 10,
	CURLOPT_RETURNTRANSFER => 1,
]);
if ($ok !== 1) {
	err("CRITICAL - curl_setopt_array should not fail");
}

$res = curl_exec($ch);
if ($res === false) {
	err("CRITICAL - curl_exec failed e=" . curl_error($ch));
}
$code = curl_getinfo($ch, CURLINFO_HTTP_CODE);
$ct = curl_getinfo($ch, CURLINFO_CONTENT_TYPE);
curl_close($ch);
if ($code !== 200) {
      err("HTTP($code) $res");
}
if (strpos($ct, "application/json") === false) {
      err("HTTP($code) invalid contentType=$ct");
}
$json = json_decode($res, true);
if (! is_array($json)) {
      err("CRITICAL - json_decode not array as expected");
}
if (VERBOSE) var_dump($json);
if (VERBOSE) echo "OK\n";

