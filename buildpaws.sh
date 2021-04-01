#! /usr/bin/env bash

WORKING_DIR="${WORKING_DIR:-${HOME}/projects/paws}"
OUTPUT_DIR="${OUTPUT_DIR:-${HOME}/public_html/paws}"
OUTPUT_NAME="${OUTPUT_NAME:-index.html}"

temp_file="paws.html"
cd "${WORKING_DIR}"
./paws >"${temp_file}" || exit 22
mv "${temp_file}" "${OUTPUT_DIR}/${OUTPUT_NAME}"
