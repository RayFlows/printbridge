#!/usr/bin/python3 -I
"""Opt-in device cancellation for the verified Canon G2010/G2810 USB profile."""
import ctypes as C
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.parse
import xml.etree.ElementTree as ET

HELPER = Path("/usr/local/libexec/printbridge-canon-cancel")
NS = "http://www.canon.com/ns/cmd/2008/07/common/"
QUERY = (
    '<?xml version="1.0" encoding="utf-8" ?><cmd xmlns:ivec="' + NS + '">'
    "<ivec:contents><ivec:operation>GetStatus</ivec:operation>"
    '<ivec:param_set servicetype="print"></ivec:param_set></ivec:contents></cmd>'
).encode("ascii")


def valid_queue(queue):
    if not re.fullmatch(r"[A-Za-z0-9_-]{1,127}", queue):
        raise ValueError("Invalid queue.")
    return queue


class Cups:
    def run(self, command, *args):
        return subprocess.run(
            ["/usr/bin/" + command, *args], check=True, capture_output=True,
            text=True, timeout=3,
            env={"PATH": "/usr/bin:/usr/sbin", "LC_ALL": "C", "CUPS_SERVER": "localhost"},
        ).stdout

    def idle(self, queue):
        if self.run("lpstat", "-W", "not-completed", "-o", queue).strip():
            raise RuntimeError("Queue has unfinished jobs.")
        # lpstat's exit status alone does not distinguish unknown/busy queues.
        if not re.search(r"^printer " + re.escape(queue) + r" is idle\.", self.run("lpstat", "-p", queue), re.M):
            raise RuntimeError("CUPS backend has not released the device.")

    def target(self, queue):
        text = self.run("lpstat", "-v", queue).strip()
        prefix = "device for " + queue + ": "
        if not text.startswith(prefix):
            raise RuntimeError("No USB target.")
        uri = urllib.parse.urlsplit(text[len(prefix):])
        args = urllib.parse.parse_qs(uri.query, strict_parsing=True)
        if (uri.scheme != "usb" or uri.netloc != "Canon" or
                urllib.parse.unquote(uri.path) != "/G2010 series" or
                set(args) - {"serial", "interface"} or
                len(args.get("serial", [])) != 1 or not args["serial"][0] or
                args.get("interface", ["1"]) != ["1"]):
            raise RuntimeError("Queue is outside the verified Canon cancellation profile.")
        return args["serial"][0]


class USB:
    def __init__(self, path, serial):
        self.lib = C.CDLL("libusb-1.0.so.0")
        self.lib.libusb_init.argtypes = [C.POINTER(C.c_void_p)]
        self.lib.libusb_open_device_with_vid_pid.argtypes = [C.c_void_p, C.c_uint16, C.c_uint16]
        self.lib.libusb_wrap_sys_device.argtypes = [C.c_void_p, C.c_ssize_t, C.POINTER(C.c_void_p)]
        self.lib.libusb_claim_interface.argtypes = [C.c_void_p, C.c_int]
        self.lib.libusb_release_interface.argtypes = [C.c_void_p, C.c_int]
        self.lib.libusb_control_transfer.argtypes = [
            C.c_void_p, C.c_uint8, C.c_uint8, C.c_uint16, C.c_uint16,
            C.c_void_p, C.c_uint16, C.c_uint,
        ]
        self.lib.libusb_bulk_transfer.argtypes = [
            C.c_void_p, C.c_ubyte, C.c_void_p, C.c_int, C.POINTER(C.c_int), C.c_uint,
        ]
        self.lib.libusb_get_string_descriptor_ascii.argtypes = [C.c_void_p, C.c_uint8, C.c_void_p, C.c_int]
        self.lib.libusb_close.argtypes = [C.c_void_p]
        self.lib.libusb_exit.argtypes = [C.c_void_p]
        self.ctx, self.handle = C.c_void_p(), C.c_void_p()
        self.fd = None
        self.claimed = False
        try:
            self.check(self.lib.libusb_init(C.byref(self.ctx)))
            self.fd = os.open(path, os.O_RDWR | os.O_CLOEXEC)
            self.check(self.lib.libusb_wrap_sys_device(self.ctx, self.fd, C.byref(self.handle)))
            descriptor = C.create_string_buffer(18)
            size = self.check(self.lib.libusb_control_transfer(
                self.handle, 0x80, 6, 0x0100, 0, descriptor, 18, 1000))
            data = descriptor.raw
            if size != 18 or data[8:12] != b"\xa9\x04\x3a\x18" or not data[16]:
                raise RuntimeError("USB descriptor changed.")
            identity = C.create_string_buffer(256)
            size = self.check(self.lib.libusb_get_string_descriptor_ascii(self.handle, data[16], identity, 256))
            if identity.raw[:size].decode("ascii") != serial:
                raise RuntimeError("USB serial changed.")
            # Never detach another driver or steal an interface from a live backend.
            self.check(self.lib.libusb_claim_interface(self.handle, 1))
            self.claimed = True
        except Exception:
            self.close()
            raise

    @staticmethod
    def check(result):
        if result < 0:
            raise RuntimeError("USB operation failed.")
        return result

    def reset(self):
        self.check(self.lib.libusb_control_transfer(self.handle, 0x21, 2, 0, 1, None, 0, 1500))

    def query(self):
        sent = C.c_int()
        self.check(self.lib.libusb_bulk_transfer(
            self.handle, 0x01, QUERY, len(QUERY), C.byref(sent), 1000))
        if sent.value != len(QUERY):
            raise RuntimeError("Partial status query.")

    def read(self):
        data, size = C.create_string_buffer(8192), C.c_int()
        result = self.lib.libusb_bulk_transfer(
            self.handle, 0x82, data, len(data), C.byref(size), 500)
        if result != -7:  # Timeouts may contain a partial report.
            self.check(result)
        return data.raw[:size.value]

    def close(self):
        if self.claimed:
            self.lib.libusb_release_interface(self.handle, 1)
        if self.handle:
            self.lib.libusb_close(self.handle)
        if self.fd is not None:
            os.close(self.fd)
        if self.ctx:
            self.lib.libusb_exit(self.ctx)


def device_path(serial, root=Path("/sys/bus/usb/devices")):
    matches = []
    for path in root.iterdir():
        try:
            if ((path / "idVendor").read_text().strip() != "04a9" or
                    (path / "idProduct").read_text().strip() != "183a" or
                    (path / "serial").read_text().strip() != serial):
                continue
            interface = path / (path.name + ":1.1")
            if (int((interface / "bInterfaceClass").read_text(), 16) != 7 or
                    int((interface / "bInterfaceSubClass").read_text(), 16) != 1 or
                    not (interface / "ep_01").is_dir() or not (interface / "ep_82").is_dir()):
                continue
            bus = int((path / "busnum").read_text())
            device = int((path / "devnum").read_text())
            if not 1 <= bus <= 999 or not 1 <= device <= 127:
                continue
            matches.append(f"/dev/bus/usb/{bus:03d}/{device:03d}")
        except (OSError, ValueError):
            continue
    if len(matches) != 1:
        raise RuntimeError("Matching Canon USB device is missing or ambiguous.")
    return matches[0]


def idle_report(frame):
    try:
        if b"<!" in frame:
            return False
        root = ET.fromstring(frame)
        if root.tag != "cmd":
            return False
        values = {}
        for element in root.iter():
            if element.tag.startswith("{" + NS + "}"):
                key = element.tag.split("}", 1)[1]
                if key not in {"operation", "response", "status", "status_detail", "jobID", "current_support_code"}:
                    continue
                if key in values or len(element):
                    return False
                values[key] = (element.text or "").strip()
        return (values.get("operation") == "GetStatusResponse" and
                values.get("response") == "OK" and values.get("status") == "idle" and
                values.get("status_detail") == "" and not values.get("jobID") and
                values.get("current_support_code") == "")
    except ET.ParseError:
        return False


def cancel(queue, cups, locate=device_path, connect=USB, clock=time.monotonic, pause=time.sleep):
    valid_queue(queue)
    cups.idle(queue)
    serial = cups.target(queue)
    usb = connect(locate(serial), serial)
    try:
        # Claiming the interface prevents a new backend from writing during reset.
        # Recheck jobs and URI after the claim, including external CUPS clients.
        cups.idle(queue)
        if cups.target(queue) != serial:
            raise RuntimeError("Queue target changed.")
        usb.reset()
        usb.query()
        deadline, buffer, consecutive = clock() + 8, b"", 0
        while clock() < deadline:
            buffer += usb.read()
            if len(buffer) > 65536:
                raise RuntimeError("Oversized device status.")
            while b"</cmd>" in buffer:
                end = buffer.index(b"</cmd>") + len(b"</cmd>")
                frame, buffer = buffer[:end], buffer[end:]
                start = frame.find(b"<cmd")
                consecutive = consecutive + 1 if start >= 0 and idle_report(frame[start:]) else 0
                if consecutive >= 2:
                    return
            pause(0.25)
        raise RuntimeError("Device did not confirm cancellation.")
    finally:
        usb.close()


def install(user, queue):
    import pwd
    valid_queue(queue)
    if not re.fullmatch(r"[a-z_][a-z0-9_-]*[$]?", user):
        raise ValueError("Invalid service user.")
    pwd.getpwnam(user)
    Cups().idle(queue)
    Cups().target(queue)
    HELPER.parent.mkdir(parents=True, exist_ok=True)
    if HELPER.parent.is_symlink() or HELPER.is_symlink():
        raise RuntimeError("Refusing symlink installation.")
    policy = Path("/etc/sudoers.d/printbridge-canon-cancel")
    if policy.exists():
        raise RuntimeError("Cancellation policy already exists; review it before reinstalling.")
    with tempfile.TemporaryDirectory(prefix="printbridge-cancel-install-") as work:
        staged = Path(work) / "policy"
        staged.write_text(f"{user} ALL=(root) NOPASSWD: {HELPER} {queue}\n", encoding="ascii")
        subprocess.run(["/usr/sbin/visudo", "-cf", str(staged)], check=True,
                       capture_output=True, timeout=5)
        shutil.copyfile(__file__, HELPER)
        os.chown(HELPER, 0, 0)
        HELPER.chmod(0o755)
        shutil.copyfile(staged, policy)
        os.chown(policy, 0, 0)
        policy.chmod(0o440)
    print("Installed restricted Canon cancellation helper.")


def main():
    if os.geteuid() != 0:
        raise RuntimeError("Run on the CUPS host through the restricted sudo rule.")
    if len(sys.argv) == 4 and sys.argv[1] == "--install":
        install(sys.argv[2], sys.argv[3])
        return
    if len(sys.argv) != 2:
        raise ValueError("Expected a configured queue name.")
    # Concurrent application instances cannot reset the same device simultaneously.
    import fcntl
    with open("/run/lock/printbridge-canon-cancel.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        cancel(sys.argv[1], Cups())
    print(json.dumps({"canceled": True, "deviceState": "idle"}))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, subprocess.SubprocessError):
        print("Device cancellation could not be verified.", file=sys.stderr)
        sys.exit(1)
