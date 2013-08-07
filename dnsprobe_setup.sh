#!/bin/bash

# copy the SSH keys into place
cp id_rsa* ~/.ssh

# configure the git client:
git config --global user.email probe1@gce
git config --global user.name "GCE Probe1"

# sync down the dnsprobe-data project
git clone git@github.com:asjoyner/dnsprobe-data.git
