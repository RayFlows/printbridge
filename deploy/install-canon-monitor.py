#!/usr/bin/env python3
"""Opt-in Canon IVEC monitor installation for an existing, idle local CUPS queue."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


QUERY = """{
OPERATION Get-Printer-Attributes
GROUP operation-attributes-tag
ATTR charset attributes-charset utf-8
ATTR language attributes-natural-language en
ATTR uri printer-uri $uri
ATTR keyword requested-attributes port-monitor,printer-is-accepting-jobs,printer-state
STATUS successful-ok
EXPECT port-monitor
EXPECT printer-is-accepting-jobs
EXPECT printer-state
}
"""


class Cups:
    def run(self, *args):
        env = {**os.environ, "LC_ALL": "C", "CUPS_SERVER": "localhost"}
        executable = shutil.which(args[0], path=env.get("PATH", "") + ":/usr/sbin") or args[0]
        args = [executable, *args[1:]]
        return subprocess.run(args, env=env, check=True, text=True,
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              timeout=20).stdout

    def idle(self, queue):
        if self.run("lpstat", "-W", "not-completed", "-o", queue).strip():
            raise RuntimeError("Queue has unfinished jobs; finish or cancel them before switching the monitor.")
        result = self.query(queue)
        state = re.search(r"^\s*printer-state \([^)]+\) = (\S+)\s*$", result, re.M)
        # Cancellation can hide a job while CUPS still has its backend attached.
        if not state or state[1] not in {"idle", "stopped"}:
            raise RuntimeError("Queue is still processing or its state is unknown; wait for canceled backends to exit.")

    def query(self, queue):
        with tempfile.TemporaryDirectory(prefix="printbridge-ipp-") as work:
            query = Path(work) / "query.test"
            query.write_text(QUERY, encoding="ascii")
            return self.run("ipptool", "-tv", "-T", "5",
                            "ipp://localhost/printers/" + queue, str(query))

    def attributes(self, queue):
        result = self.query(queue)
        monitor = re.search(r"^\s*port-monitor \([^)]+\) = (\S+)\s*$", result, re.M)
        accepting = re.search(r"^\s*printer-is-accepting-jobs \(boolean\) = (true|false)\s*$", result, re.M)
        if not monitor or not accepting:
            raise RuntimeError("CUPS did not return the current monitor and accepting state.")
        return monitor[1], accepting[1] == "true"

    def reject(self, queue):
        self.run("cupsreject", "-r", "PrintBridge monitor configuration", queue)

    def accept(self, queue):
        self.run("cupsaccept", queue)

    def configure(self, queue, ppd, monitor):
        # CUPS validates port-monitor against the previously loaded PPD, so the
        # declaration must be installed before the separate selection request.
        self.run("lpadmin", "-p", queue, "-P", str(ppd))
        # Re-upload also invalidates CUPS' cached PPD attributes, including the
        # selected port-monitor value returned by Get-Printer-Attributes.
        self.run("lpadmin", "-p", queue, "-P", str(ppd), "-o", "port-monitor=" + monitor)
        if self.attributes(queue)[0] != monitor:
            raise RuntimeError("CUPS did not activate the requested monitor.")


class Installer:
    def __init__(self, cups, ppds=Path("/etc/cups/ppd"),
                 monitors=Path("/usr/lib/cups/monitor"),
                 backups=Path("/var/backups/printbridge-canon-monitor")):
        self.cups, self.ppds, self.monitors, self.backups = cups, ppds, monitors, backups

    def install(self, queue, binary):
        # This check precedes all persistent changes, including backups/binaries.
        self.cups.idle(queue)
        original = (self.ppds / (queue + ".ppd")).read_bytes()
        if not re.search(rb'^\*(?:Manufacturer|ModelName):[^\r\n]*Canon', original, re.M | re.I):
            raise RuntimeError("This adapter requires an existing Canon PPD queue.")
        previous, accepting = self.cups.attributes(queue)
        content = binary.read_bytes()
        name = "printbridge-canon-" + hashlib.sha256(content).hexdigest()[:20]
        target = self.monitors / name
        if target.exists() and target.read_bytes() != content:
            raise RuntimeError("An installed monitor differs; refusing to overwrite it.")
        if previous == name and target.is_file():
            return None
        # Reject new submissions temporarily; recheck jobs that raced the first check.
        if accepting:
            self.cups.reject(queue)
        backup = None
        try:
            self.cups.idle(queue)
            self.monitors.mkdir(parents=True, exist_ok=True)
            if not target.exists():
                with tempfile.NamedTemporaryFile(dir=self.monitors, delete=False) as stage:
                    stage.write(content)
                    stage_path = Path(stage.name)
                try:
                    stage_path.chmod(0o755)
                    version = self.cups.run(str(stage_path), "--version").strip()
                    if version != "printbridge-canon-monitor 1":
                        raise RuntimeError("Unexpected monitor executable.")
                    stage_path.replace(target)
                finally:
                    stage_path.unlink(missing_ok=True)
            self.backups.mkdir(parents=True, exist_ok=True, mode=0o700)
            backup = Path(tempfile.mkdtemp(prefix=queue + "-", dir=self.backups))
            (backup / "original.ppd").write_bytes(original)
            (backup / "metadata.json").write_text(json.dumps({
                "queue": queue, "previousMonitor": previous, "installedMonitor": name,
            }), encoding="ascii")
            # Retain the manufacturer's filters and every existing setting.
            modified = re.sub(rb'^\*cupsPortMonitor printbridge-canon-[^\r\n]*[\r\n]*', b"",
                              original, flags=re.M)
            modified += ('\n*cupsPortMonitor ' + name + '/PrintBridge Canon status: "' + name + '"\n').encode("ascii")
            (backup / "monitor.ppd").write_bytes(modified)
            try:
                self.cups.configure(queue, backup / "monitor.ppd", name)
                (backup / "activated.ppd").write_bytes((self.ppds / (queue + ".ppd")).read_bytes())
            except Exception:
                self.cups.configure(queue, backup / "original.ppd", previous)
                raise
            return backup
        finally:
            if accepting:
                self.cups.accept(queue)

    def rollback(self, backup):
        metadata = json.loads((backup / "metadata.json").read_text(encoding="ascii"))
        queue = valid_queue(metadata["queue"])
        self.cups.idle(queue)
        current, accepting = self.cups.attributes(queue)
        if current != metadata["installedMonitor"]:
            raise RuntimeError("Queue monitor changed since this backup; refusing to overwrite newer settings.")
        current_ppd = (self.ppds / (queue + ".ppd")).read_bytes()
        if current_ppd != (backup / "activated.ppd").read_bytes():
            raise RuntimeError("Queue PPD changed since this backup; review those settings before rollback.")
        if accepting:
            self.cups.reject(queue)
        try:
            self.cups.idle(queue)
            self.cups.configure(queue, backup / "original.ppd", metadata["previousMonitor"])
        finally:
            if accepting:
                self.cups.accept(queue)
        # Immutable binaries can be shared by other queues and are retained.


def valid_queue(value):
    if not re.fullmatch(r"[a-zA-Z0-9_-]+", value):
        raise ValueError("Invalid queue name.")
    return value


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="action", required=True)
    install = commands.add_parser("install")
    install.add_argument("--queue", required=True, type=valid_queue)
    install.add_argument("--binary", required=True, type=Path)
    rollback = commands.add_parser("rollback")
    rollback.add_argument("backup", type=Path)
    args = parser.parse_args()
    if not hasattr(os, "geteuid") or os.geteuid() != 0:
        parser.error("Run with sudo on the CUPS host.")
    import fcntl
    with open("/run/lock/printbridge-canon-monitor.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        installer = Installer(Cups())
        if args.action == "install":
            backup = installer.install(args.queue, args.binary.resolve(strict=True))
            print("Monitor already selected." if backup is None else "Monitor installed. Rollback backup: " + str(backup))
        else:
            installer.rollback(args.backup.resolve(strict=True))
            print("Previous PPD and monitor restored.")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError) as error:
        # Subprocess output can contain queue/driver configuration. Keep it private.
        message = str(error) if isinstance(error, (ValueError, RuntimeError)) else type(error).__name__
        print("Installation stopped: " + message, file=sys.stderr)
        sys.exit(1)
