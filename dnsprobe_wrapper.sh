#!/bin/bash

while [ 1 ]; do
  ./dnsprobe -m 30s -s 30s --address ":5353" -u
  sleep 1
done
