#!/usr/bin/env sh

# Options
PLATFORM_OPT="<platform>"
ARCH_OPT="<arch>"

# Usage
usage() {
  echo "Usage:"
  echo "    $0 $PLATFORM_OPT $ARCH_OPT"
  echo "where:"
  printf "%15s  target platform: linux, win32, darwin.\n" $PLATFORM_OPT
  printf "%15s  target architecture: x64, arm64.\n" $ARCH_OPT
  exit 1;
}

# Colors
YEL='\033[1;33m'
RED='\033[0;31m'
GRE='\033[0;32m'
NC='\033[0m'

# Check number of arguments
if [ "$#" -ne 2 ]; then
  echo "${RED}ERROR:${NC} Incorrect number of arguments."
  usage;
  exit 1;
fi

# Check platform
if [ "$1" != "linux" ] && [ "$1" != "win32" ] && [ "$1" != "darwin" ]; then
  echo "${RED}ERROR:${NC} unsupported platform '$1'."
  usage;
  exit 1;
fi

# Check architecture
if [ "$2" != "x64" ] && [ "$2" != "arm64" ]; then
  echo "${RED}ERROR:${NC} unsupported architecture '$2'."
  usage;
  exit 1;
fi

# Build variables
NPM_SCRIPT="build_$1_$2"
OLD_BUILD_DIR="build/grabber-$1-$2"
NEW_BUILD_DIR="build/webrtc_grabber_$1_$2"

# Move to working directory
cd packages/grabber

# Remove old build directory
if [ -d "build" ]; then
  rm -rf $NEW_BUILD_DIR;
fi;

# Run npm build script
NPM_RUN_CMD="npm run $NPM_SCRIPT"
NPM_INSTALL_CMD="npm ci"
if ! eval $NPM_RUN_CMD; then
  echo "${RED}FAILED:${NC} $NPM_RUN_CMD"
  echo "${YEL}Trying to install packages...${NC}"
  if eval $NPM_INSTALL_CMD; then
    echo "${GRE}SUCCESSFUL${NC}"
    echo "${YEL}Trying to build grabber...${NC}"
    if ! eval $NPM_RUN_CMD; then
      echo "${RED}FAILED:${NC} $NPM_RUN_CMD"
      exit 1;
    fi
  else
    echo "${RED}FAILED:${NC} $NPM_INSTALL_CMD"
    exit 1;
  fi
fi

# Finish build
eval "mv $OLD_BUILD_DIR $NEW_BUILD_DIR"
if [ "$1" = "linux" ] || [ "$1" = "darwin" ]; then
  eval "cp -r scripts/grabber-$1.sh $NEW_BUILD_DIR"
else
  eval "cp -r scripts/*.bat $NEW_BUILD_DIR"
fi
echo "${GRE}BUILD SUCCESSFUL${NC}"
