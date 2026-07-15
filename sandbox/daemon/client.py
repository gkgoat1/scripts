#!/usr/bin/env python3
"""Minimal sandbox-daemon control client."""
import argparse
import os
import socket
import sys

parser = argparse.ArgumentParser()
parser.add_argument("--socket", required=True)
parser.add_argument("command", choices=("ping", "list", "kill", "killall"))
parser.add_argument("argument", nargs="?")
args = parser.parse_args()

request = args.command.upper()
if args.argument:
    request += " " + args.argument
try:
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as client:
        client.connect(args.socket)
        client.sendall((request + "\n").encode())
        sys.stdout.buffer.write(client.recv(65536))
except OSError as error:
    print(f"sandbox: daemon unavailable: {error}", file=sys.stderr)
    sys.exit(1)