#!/usr/bin/env bash
# P5 (Linux x64): SHA-256 throughput per tool, then cAdvisor on native Linux.
# Usage: refactor/live-tests/p5-sha-cadvisor.sh [WORKDIR]   (default: a mktemp dir; needs ~2.2 GB free)
# Compare with the macOS arm64 / Alpine numbers in refactor/15-verification-log.md.
set -euo pipefail
W=${1:-$(mktemp -d)}; F=$W/bench.bin; SIZE_MB=2048
echo "workdir: $W"; uname -srm; nproc
[ -f "$F" ] || dd if=/dev/urandom of="$F" bs=1M count=$SIZE_MB status=none
t() { local name=$1; shift; local s e; s=$(date +%s.%N); "$@" >/dev/null; e=$(date +%s.%N)
      awk -v n="$name" -v s="$s" -v e="$e" -v m="$SIZE_MB" 'BEGIN{printf "%-28s %6.0f MB/s\n", n, m/(e-s)}'; }
cat "$F" >/dev/null   # warm page cache so the disk isn't measured
command -v sha256sum >/dev/null && t "coreutils sha256sum" sha256sum "$F"
command -v openssl   >/dev/null && t "openssl dgst -sha256" openssl dgst -sha256 "$F"
command -v shasum    >/dev/null && t "perl shasum -a 256" shasum -a 256 "$F"
# Same file inside containers (read through a bind mount; Linux allows it).
t "busybox sha256sum (alpine)" docker run --rm -v "$W:/w:ro" alpine:3.20 sha256sum /w/bench.bin
t "coreutils (alpine)"         docker run --rm -v "$W:/w:ro" alpine:3.20 sh -c 'apk add -q coreutils >/dev/null && /usr/bin/sha256sum /w/bench.bin'
cat > "$W/sha.go" <<'EOF'
package main
import ("crypto/sha256";"fmt";"io";"os")
func main(){f,_:=os.Open(os.Args[1]);h:=sha256.New();io.Copy(h,f);fmt.Printf("%x\n",h.Sum(nil))}
EOF
docker run --rm -v "$W:/w" -w /w golang:1.25 go build -o sha-go sha.go
t "go crypto/sha256"           "$W/sha-go" "$F"
openssl speed -evp sha256 2>/dev/null | tail -1 || true

echo; echo "== cAdvisor v0.52.1 (native Linux)"
docker rm -f sbd-p5-cadvisor >/dev/null 2>&1 || true
docker run -d --name sbd-p5-cadvisor -p 127.0.0.1:28080:8080 --privileged --device=/dev/kmsg \
  -v /:/rootfs:ro -v /var/run:/var/run:ro -v /sys:/sys:ro -v /var/lib/docker/:/var/lib/docker:ro -v /dev/disk/:/dev/disk:ro \
  gcr.io/cadvisor/cadvisor:v0.52.1 --housekeeping_interval=15s --docker_only=true \
  --disable_metrics=percpu,sched,tcp,udp,hugetlb,referenced_memory,cpu_topology,resctrl,advtcp >/dev/null
sleep 45
stat -fc 'cgroup fs: %T' /sys/fs/cgroup
curl -s 127.0.0.1:28080/metrics | grep '^# TYPE container_' | awk '{print $3}' | sort | tr '\n' ' '; echo
curl -s 127.0.0.1:28080/metrics | grep -cE '^container_(cpu_usage_seconds_total|memory_working_set_bytes|fs_reads_bytes_total|blkio_device_usage_total)' || true
docker stats --no-stream --format '{{.Name}} cpu={{.CPUPerc}} mem={{.MemUsage}}' sbd-p5-cadvisor
docker rm -f sbd-p5-cadvisor >/dev/null
echo "done; remove $W when finished"
