#!/bin/sh
set -eu
mkdir -p build
go run ./cmd/testvectors --out build/vector.bin
cc -std=c99 -Wall -Wextra -Werror -I sdk/c -I sdk/third_party/monocypher/src -I sdk/third_party/monocypher/src/optional \
  tests/sdk_test.c sdk/c/fw_client.c sdk/third_party/monocypher/src/monocypher.c sdk/third_party/monocypher/src/optional/monocypher-ed25519.c -o build/sdk-test
build/sdk-test build/vector.bin
c++ -std=c++17 -Wall -Wextra -Werror -c sdk/cpp/fw_client.cpp -o build/cpp-sdk.o
