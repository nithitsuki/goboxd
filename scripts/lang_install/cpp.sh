#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/helpers.sh"

if ! command -v g++ &>/dev/null; then
    echo "g++ is not installed. Installing g++..."
    pkg_install g++
fi

temp_file=$(mktemp /tmp/test_cpp.XXXXXX.cpp)
cat <<EOF > "$temp_file"
#include <iostream>
int main() {
    std::cout << "C++ is working!" << std::endl;
    return 0;
}
EOF

output_file=$(mktemp /tmp/test_cpp.XXXXXX.out)
g++ "$temp_file" -o "$output_file"

if "$output_file"; then
    echo "C++ installation verified successfully."
else
    echo "C++ installation verification failed."
    exit 1
fi

rm -f "$temp_file" "$output_file"
