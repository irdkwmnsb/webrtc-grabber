#!/usr/bin/env sh

# Options
ACTION_OPT="<action>"
PEER_OPT="<peer_name>"
SIGNALING_URL_OPT="<signaling_url>"

# Colors
RED='\033[0;31m'
GRE='\033[0;32m'
NC='\033[0m'

# Usage
usage() {
  echo "Usage:"
  echo "    $0 $ACTION_OPT [$PEER_OPT $SIGNALING_URL_OPT]"
  echo "where:"
  printf "%20s desired action: run, test, stop. \n" $ACTION_OPT
  printf "%20s peer name from a signaling server. \n" $PEER_OPT
  printf "%20s URL link to a signaling server. \n" $SIGNALING_URL_OPT
  exit 1;
}

# RUN
run_grabber() {
  # $4 is used only for testing. It is '--debugMode', if $4 is present.
  if eval "$(dirname "$0")/grabber.app/Contents/MacOS/grabber . --peerName=\"$2\" --signalingUrl=\"$3\" $4"; then
    echo "${GRE}SUCCESS:${NC} grabber is run."
    exit 0;
  else
    echo "${RED}Failed to run grabber.${NC}"
    exit 1;
  fi
}

# STOP
stop_grabber() {
  if eval "pkill grabber"; then
    echo "${GRE}Grabber was stopped.${NC}"
    exit 0;
  else
    echo "${RED}Failed to kill grabber.${NC}"
    exit 1;
  fi
}

# Check number of arguments
if [ "$#" -ne 1 ] && [ "$#" -ne 3 ]; then
  echo "${RED}ERROR:${NC} Incorrect number of arguments."
  usage;
  exit 1;
fi

# Check that action is 'stop'
if [ "$#" = 1 ]; then
  if [ "$1" = "stop" ]; then
    stop_grabber;
  else
    echo "${RED}ERROR:${NC} Incorrect action '$1'."
    usage;
    exit 1;
  fi
fi

# Check that action is 'run' or 'test'
if [ "$#" = 3 ]; then
  if [ "$1" = "run" ]; then
    run_grabber "$1" "$2" "$3";
  elif [ "$1" = "test" ]; then
    run_grabber "$1" "$2" "$3" "--debugMode";
    exit 1;
  else
    echo "${RED}ERROR:${NC} Incorrect action '$1'."
    exit 1;
  fi
fi
