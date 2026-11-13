#!/bin/sh
set -eu
mkdir -p build
go run ./cmd/testvectors --out build/vector.bin
cc -std=c99 -Wall -Wextra -Werror -I sdk/c -I sdk/third_party/monocypher/src -I sdk/third_party/monocypher/src/optional \
  tests/sdk_test.c sdk/c/fw_client.c sdk/third_party/monocypher/src/monocypher.c sdk/third_party/monocypher/src/optional/monocypher-ed25519.c -o build/sdk-test
build/sdk-test build/vector.bin
c++ -std=c++17 -Wall -Wextra -Werror -c sdk/cpp/fw_client.cpp -o build/cpp-sdk.o
cc -std=c99 -Wall -Wextra -Werror -I sdk/rs485 tests/rs485_test.c sdk/rs485/fw485.c -o build/rs485-test
build/rs485-test
cc -std=c99 -Wall -Wextra -Werror -Wno-int-to-pointer-cast -I sdk/rs485 -I sdk/third_party/tinycrypt/include \
  tests/n32_boot_test.c sdk/rs485/fw485.c sdk/third_party/tinycrypt/src/sha256.c sdk/third_party/tinycrypt/src/utils.c -o build/n32-boot-test
build/n32-boot-test
