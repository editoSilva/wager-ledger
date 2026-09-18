#!/bin/bash
set -euo pipefail

mkdir -p /home/tester/.ssh
cp /run/secrets/mmt-authorized-keys /home/tester/.ssh/authorized_keys
chown -R tester:tester /home/tester/.ssh
chmod 700 /home/tester/.ssh
chmod 600 /home/tester/.ssh/authorized_keys

exec /usr/sbin/sshd -D -e
