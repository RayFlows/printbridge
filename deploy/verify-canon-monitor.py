#!/usr/bin/env python3
"""Verify the adapter using a separate CUPS scheduler and a synthetic backend only."""
import argparse
import grp
import importlib.util
import os
from pathlib import Path
import pwd
import re
import shutil
import socket
import subprocess
import tempfile
import time

spec = importlib.util.spec_from_file_location("installer", Path(__file__).with_name("install-canon-monitor.py"))
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

PPD = """*PPD-Adobe: "4.3"
*FormatVersion: "4.3"
*FileVersion: "1.0"
*LanguageVersion: English
*LanguageEncoding: ISOLatin1
*PCFileName: "VIRTUAL.PPD"
*Manufacturer: "Canon"
*Product: "(Virtual)"
*ModelName: "Canon Virtual"
*NickName: "Canon Virtual"
*PSVersion: "(3010.000) 0"
*LanguageLevel: "3"
*ColorDevice: False
*DefaultColorSpace: Gray
*FileSystem: False
*Throughput: "1"
*cupsFilter: "application/pdf 0 -"
*OpenUI *PageSize: PickOne
*DefaultPageSize: A4
*PageSize A4: "<</PageSize[595 842]>>setpagedevice"
*CloseUI: *PageSize
*DefaultImageableArea: A4
*ImageableArea A4: "0 0 595 842"
*DefaultPaperDimension: A4
*PaperDimension A4: "595 842"
"""

BACKEND = r'''#!/usr/bin/python3
import os
from pathlib import Path
import signal
import sys
import time
if len(sys.argv) == 1:
    print('direct bridgefake:/ "Virtual" "PrintBridge synthetic backend"')
    sys.exit(0)
root = Path(__file__).resolve().parents[2]
delay_cancel = (root / "delay-cancel").exists()
if delay_cancel:
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
data = Path(sys.argv[6]).read_bytes() if len(sys.argv) > 6 else sys.stdin.buffer.read()
(root / "received").write_bytes(data)
def report(status, detail):
    payload = ('<cmd xmlns:ivec="urn:canon:ivec"><ivec:contents>'
        '<ivec:operation>GetStatusResponse</ivec:operation><ivec:response>OK</ivec:response>'
        '<ivec:status>' + status + '</ivec:status><ivec:status_detail>' + detail +
        '</ivec:status_detail></ivec:contents></cmd>').encode()
    for offset in range(0, len(payload), 7):
        os.write(3, payload[offset:offset+7])
if not delay_cancel:
    report('stopped', 'MediaEmpty')
deadline = time.monotonic() + 30
while not (root / "recover").exists() and time.monotonic() < deadline:
    time.sleep(0.02)
if not delay_cancel:
    report('processing', '')
while not (root / "finish").exists() and time.monotonic() < deadline:
    time.sleep(0.02)
'''


def until(predicate, description, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if predicate():
                return
        except (OSError, subprocess.SubprocessError):
            pass
        time.sleep(0.05)
    raise RuntimeError("Timed out: " + description)


class VirtualCups(installer.Cups):
    def __init__(self, port):
        self.address = "127.0.0.1:" + str(port)

    def run(self, *args):
        args = [arg.replace("ipp://localhost/", "ipp://" + self.address + "/") for arg in args]
        args[0] = shutil.which(args[0], path=os.environ.get("PATH", "") + ":/usr/sbin") or args[0]
        return subprocess.run(args, env={**os.environ, "LC_ALL": "C", "CUPS_SERVER": self.address},
                              check=True, text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              timeout=15).stdout


def verify(binary, printing_test=None, cancel_delay=None):
    if os.geteuid() == 0:
        raise RuntimeError("Run this isolated check as a normal user, without sudo.")
    cupsd = shutil.which("cupsd") or "/usr/sbin/cupsd"
    with tempfile.TemporaryDirectory(prefix="printbridge-virtual-cups-") as work:
        root = Path(work)
        for name in ["bin/backend", "bin/monitor", "bin/daemon", "state", "cache", "spool", "tmp", "ppd", "share/mime"]:
            (root / name).mkdir(parents=True, exist_ok=True)
        (root / "share/mime/mime.types").write_text(
            "application/vnd.cups-raw\napplication/octet-stream\napplication/postscript\napplication/pdf\n", encoding="ascii")
        for executable in ["cups-exec"]:
            shutil.copy2("/usr/lib/cups/daemon/" + executable, root / "bin/daemon" / executable)
        backend = root / "bin/backend/bridgefake"
        backend.write_text(BACKEND, encoding="ascii")
        backend.chmod(0o755)
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            port = listener.getsockname()[1]
        files = root / "cups-files.conf"
        files.write_text("\n".join([
            "User " + pwd.getpwuid(os.getuid()).pw_name,
            "Group " + grp.getgrgid(os.getgid()).gr_name,
            "SystemGroup " + grp.getgrgid(os.getgid()).gr_name,
            "ServerRoot " + work, "ServerBin " + str(root / "bin"),
            "DataDir " + str(root / "share"), "StateDir " + str(root / "state"),
            "RequestRoot " + str(root / "spool"), "CacheDir " + str(root / "cache"),
            "TempDir " + str(root / "tmp"), "ErrorLog " + str(root / "error.log"),
            "AccessLog " + str(root / "access.log"), "PageLog " + str(root / "page.log"),
        ]) + "\n", encoding="ascii")
        config = root / "cupsd.conf"
        config.write_text(f"""Listen 127.0.0.1:{port}
Browsing Off
WebInterface No
LogLevel debug
PreserveJobFiles No
DefaultEncryption Never
<Location />
Order allow,deny
Allow localhost
</Location>
<Policy default>
<Limit All>
Order allow,deny
Allow localhost
</Limit>
</Policy>
""", encoding="ascii")
        if cancel_delay is not None:
            with config.open("a", encoding="ascii") as stream:
                stream.write(f"JobKillDelay {cancel_delay}\n")
        with (root / "daemon.log").open("w") as daemon_log:
            process = subprocess.Popen([cupsd, "-f", "-c", str(config), "-s", str(files)],
                                       stdout=daemon_log, stderr=daemon_log)
            try:
                cups = VirtualCups(port)
                until(lambda: "running" in cups.run("lpstat", "-r"), "virtual CUPS startup")
                ppd = root / "source.ppd"
                ppd.write_text(PPD, encoding="ascii")
                cups.run("lpadmin", "-p", "virtual", "-E", "-v", "bridgefake:/", "-P", str(ppd))
                adapter = installer.Installer(cups, root / "ppd", root / "bin/monitor", root / "backups")
                # A canceled job can disappear from Get-Jobs before its backend exits.
                (root / "delay-cancel").touch()
                canceled_source = root / "canceled.raw"
                canceled_source.write_bytes(b"synthetic cancellation fixture")
                submitted = cups.run("lp", "-d", "virtual", "-o", "raw", str(canceled_source))
                canceled_job = re.search(r"virtual-\d+", submitted)
                if not canceled_job:
                    raise RuntimeError("Virtual submission did not return a job ID.")
                until(lambda: (root / "received").exists(), "cancellation fixture backend")
                cups.run("cancel", canceled_job[0])
                until(lambda: not cups.run("lpstat", "-W", "not-completed", "-o", "virtual").strip(),
                      "canceled job removed from active list")
                if "now printing" not in cups.run("lpstat", "-p", "virtual"):
                    raise RuntimeError("Cancellation fixture did not leave the printer processing.")
                try:
                    adapter.install("virtual", binary)
                    raise AssertionError("Installer accepted a canceled job whose backend is still processing.")
                except RuntimeError as error:
                    if "processing" not in str(error):
                        raise
                if (root / "backups").exists():
                    raise RuntimeError("Cancellation guard made persistent configuration changes.")
                canceled_at = time.monotonic()
                if cancel_delay is None:
                    (root / "recover").touch()
                    (root / "finish").touch()
                until(lambda: "is idle" in cups.run("lpstat", "-p", "virtual"), "canceled backend exit",
                      timeout=(cancel_delay + 12) if cancel_delay is not None else 15)
                if cancel_delay is not None:
                    elapsed = time.monotonic() - canceled_at
                    if elapsed > cancel_delay + 11:
                        raise RuntimeError("Canceled backend exceeded the configured termination deadline.")
                    print(f"PASS: scheduler terminated canceled, SIGTERM-ignoring backend in {elapsed:.2f}s")
                if cups.run("lpstat", "-W", "not-completed", "-o", "virtual").strip():
                    raise RuntimeError("Canceled job was restarted.")
                for marker in ["delay-cancel", "received", "recover", "finish"]:
                    (root / marker).unlink(missing_ok=True)
                print("PASS: canceled job with a live backend refuses configuration without restarting")
                backup = adapter.install("virtual", binary)
                if adapter.install("virtual", binary) is not None:
                    raise RuntimeError("Repeated installation changed the queue.")
                query = root / "reasons.test"
                query.write_text(installer.QUERY.replace(
                    "port-monitor,printer-is-accepting-jobs", "printer-state-reasons"
                ).replace("EXPECT port-monitor\nEXPECT printer-is-accepting-jobs", "EXPECT printer-state-reasons"),
                    encoding="ascii")
                def reasons():
                    return cups.run("ipptool", "-tv", "-T", "3", "ipp://localhost/printers/virtual", str(query))
                payload = bytes(range(256)) * 256
                source = root / "fixture.raw"
                source.write_bytes(payload)
                cups.run("lp", "-d", "virtual", "-o", "raw", str(source))
                until(lambda: "media-empty-error" in reasons(), "CUPS paper-out reason")
                print("PASS: fragmented back-channel -> CUPS media-empty-error")
                try:
                    adapter.install("virtual", binary)
                    raise AssertionError("Installer accepted an unfinished job.")
                except RuntimeError as error:
                    if "unfinished jobs" not in str(error):
                        raise
                (root / "recover").touch()
                until(lambda: "media-empty" not in reasons(), "CUPS recovery")
                print("PASS: recovered back-channel clears CUPS paper-out reason")
                (root / "finish").touch()
                until(lambda: not cups.run("lpstat", "-W", "not-completed", "-o", "virtual").strip(),
                      "virtual job completion")
                if (root / "received").read_bytes() != payload:
                    raise RuntimeError("Monitor changed the print bytes.")
                adapter.rollback(backup)
                if cups.attributes("virtual")[0] != "none":
                    raise RuntimeError("Monitor rollback failed.")
                print("PASS: unchanged print bytes, idle-only installation, repeat install and rollback")
                if printing_test is not None:
                    for marker in ["received", "recover", "finish"]:
                        (root / marker).unlink(missing_ok=True)
                    result = subprocess.run(
                        [str(printing_test), "-test.run=^TestCancellationAgainstIsolatedCUPS$", "-test.v"],
                        env={**os.environ, "PRINTBRIDGE_TEST_CUPS_ROOT": str(root),
                             "PRINTBRIDGE_TEST_CANCEL_DELAY": str(cancel_delay or ""),
                             "PRINTBRIDGE_TEST_CUPS_URL": "http://" + cups.address},
                        text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=45)
                    print(result.stdout)
                    result.check_returncode()
            except Exception as error:
                # This isolated scheduler contains synthetic data only.
                if isinstance(error, subprocess.CalledProcessError):
                    print(error.stderr)
                for log in ["daemon.log", "error.log"]:
                    path = root / log
                    if path.exists():
                        print(path.read_text(errors="replace")[-16000:])
                raise
            finally:
                (root / "recover").touch()
                (root / "finish").touch()
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--printing-test", type=Path, help="Optional compiled printing test executable")
    parser.add_argument("--cancel-delay", type=int, choices=range(1, 11),
                        help="Exercise scheduler termination without cooperative backend exit")
    args = parser.parse_args()
    verify(args.binary.resolve(strict=True),
           args.printing_test.resolve(strict=True) if args.printing_test else None, args.cancel_delay)
