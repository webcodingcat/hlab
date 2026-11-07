"""P0-10: latency of Hyper-V commands: process per command vs. one long-running PowerShell."""
import json
import subprocess
import time

PS = "/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe"


def timed(fn, n):
    samples = []
    for _ in range(n):
        start = time.perf_counter()
        fn()
        samples.append((time.perf_counter() - start) * 1000)
    samples.sort()
    return f"median {samples[len(samples) // 2]:.0f} ms, max {samples[-1]:.0f} ms (n={n})"


print("process per command (powershell.exe -Command Get-VM):",
      timed(lambda: subprocess.run([PS, "-NoProfile", "-Command", "(Get-VM deb-gc).State"],
                                   capture_output=True, cwd="/mnt/c"), 5))

host = subprocess.Popen([PS, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", r"C:\HLabSpike\scripts\pshost.ps1"],
                        stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True, cwd="/mnt/c")


def call(request):
    host.stdin.write(json.dumps(request) + "\n")
    host.stdin.flush()
    return json.loads(host.stdout.readline())


start = time.perf_counter()
call({"op": "state", "name": "deb-gc"})
print(f"long-running PowerShell, first call incl. start and module load: {(time.perf_counter() - start) * 1000:.0f} ms")
print("long-running PowerShell, state:", timed(lambda: call({"op": "state", "name": "deb-gc"}), 20))
print("long-running PowerShell, list by Notes marker:", timed(lambda: call({"op": "list"}), 10))
for i in range(3):
    start = time.perf_counter()
    answer = call({"op": "create", "name": f"p010-{i}"})
    print(f"create from differencing VHDX until '{answer.get('result')}': {(time.perf_counter() - start) * 1000:.0f} ms")
for i in range(3):
    call({"op": "delete", "name": f"p010-{i}"})
host.stdin.close()
