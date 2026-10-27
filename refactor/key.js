#!/usr/bin/env node
import { StacksTestnet, StacksMainnet } from '@stacks/network';

const EC = require("elliptic").ec;
const secp256k1 = new EC("secp256k1");
const enc = require("@stacks/encryption");
const process = require("process");
let key;

while (true) {
  // draw a fresh key each iteration; a 63-hex key (leading zero nibble) is retried
  key = secp256k1.genKeyPair();
  const key_hex = key.getPrivate().toString("hex");
  if (key_hex.length != 64) {
    continue;
  }
  break;
}
try {
  const privateKey = key.getPrivate().toString("hex");
  const publicKey = enc.getPublicKeyFromPrivate(privateKey);
  process.stdout.write([publicKey, ":", privateKey].join(""));
} catch {
  return process.exit(1);
}
return process.exit(0);
