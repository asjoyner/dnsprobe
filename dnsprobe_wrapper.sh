#!/bin/bash

while [ 1 ]; do
  ./dnsprobe -m 30s -s 30s --address ":8080" -u
  sleep 1
done
