#!/bin/bash
arg="${1:-pro}"

# Build first while the old process is still running
case "$arg" in
    run|RUN|run-osc|RUN-OSC)
        # No build needed
        ;;
    skip|SKIP)
        make cli pro WITH_REACT=OFF
        ;;
    v2|V2)
        make cli pro osc WITH_REACT=OFF
        ;;
    osc|OSC)
        make cli osc
        ;;
    *)
        make cli pro
        ;;
esac

# Stop the running process
./stop.sh

# Start with "run" flag to skip rebuild
case "$arg" in
    osc|OSC|run-osc|RUN-OSC|v2|V2)
        ./run.sh run-osc
        ;;
    *)
        ./run.sh run
        ;;
esac
