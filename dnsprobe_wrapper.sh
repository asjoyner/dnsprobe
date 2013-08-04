#!/bin/bash

while [ 1 ]; do
  ./dnsprobe -m 10s -s 60s --address ":8080" -u
  sleep 1
done
