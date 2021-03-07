#! /bin/bash

function die () {
	echo "ERROR: $*"
	exit 1
}

if [ -z "${SSH_ID}" ]; then die "no SSH_ID"; fi
if [ ! -f "${SSH_ID}" ]; then die "SSH_ID ${SSH_ID} does not exist."; fi
if [ -z "${SSH_USER}" ]; then die "no SSH_USER"; fi
if [ -z "${SSH_HOST}" ]; then die "no SSH_HOST"; fi
if [ -z "${HTML_PATH}" ]; then die "no HTML_PATH"; fi

# assume we fail.
success=/bin/false

# prep the directory
make -q
./petids >./index.html && success=/bin/true

# if we suceeded, copy the file.
if $success; then
	/home/oliver/bin/sfput.py main.go template.txt index.html style.css
fi
