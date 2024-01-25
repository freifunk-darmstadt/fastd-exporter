#!/usr/bin/env bash

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# names of fastd instanes to start
fastd_instances=("dom0" "dom1" "dom2")

# Array to hold the PIDs of the fastd instances
fastd_pids=()

# Stop the fastd instances when the script exits
trap 'for pid in "${fastd_pids[@]}"; do kill $pid; done' EXIT

# Start the fastd instances
for instance in "${fastd_instances[@]}"
do
    fastd --config $SCRIPT_DIR/fastd/$instance/fastd.conf --status-socket $SCRIPT_DIR/fastd-$instance.sock 2>&1 | sed "s/^/[fastd $instance] /" &
    # Save the PID of the fastd instance
    fastd_pids+=($!)
done

# Wait for the fastd instances to start
sleep 1

# Generate the string of instances and their corresponding sockets
fastd_exporter_socket=""
for instance in "${fastd_instances[@]}"
do
    fastd_exporter_socket+="$instance=$SCRIPT_DIR/fastd-$instance.sock "
done

# Start fastd-exporter
${SCRIPT_DIR}/../fastd-exporter -ipisp.timeout=100 $fastd_exporter_socket  2>&1 | sed "s/^/[exporter] /"
