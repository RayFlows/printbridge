import importlib.util
import os
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("canon_cancel", Path(__file__).parents[1] / "canon-cancel.py")
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


def report(status="idle", detail="", job="", support=""):
    return (f'<cmd xmlns:ivec="{adapter.NS}"><ivec:contents>'
            '<ivec:operation>GetStatusResponse</ivec:operation><ivec:response>OK</ivec:response>'
            f"<ivec:status>{status}</ivec:status><ivec:status_detail>{detail}</ivec:status_detail>"
            f"<ivec:current_support_code>{support}</ivec:current_support_code>"
            f"{'<ivec:jobID>' + job + '</ivec:jobID>' if job else ''}"
            "</ivec:contents></cmd>").encode()


class Cups:
    def __init__(self, events):
        self.events, self.checks = events, 0
        self.fail_check = 0
        self.change_target = False

    def idle(self, queue):
        self.events.append("guard")
        self.checks += 1
        if self.checks == self.fail_check:
            raise RuntimeError("busy")

    def target(self, queue):
        return "changed" if self.change_target and self.checks == 2 else "synthetic-serial"


class USB:
    def __init__(self, events, frames):
        self.events, self.frames = events, iter(frames)

    def reset(self): self.events.append("reset")
    def query(self): self.events.append("query")
    def read(self): return next(self.frames, b"")
    def close(self): self.events.append("close")


class Cancellation(unittest.TestCase):
    def run_cancel(self, cups, frames):
        usb = USB(cups.events, frames)
        tick = iter(range(30))
        adapter.cancel("Virtual", cups, locate=lambda serial: "synthetic-device",
                       connect=lambda path, serial: cups.events.append("claim") or usb,
                       clock=lambda: next(tick), pause=lambda seconds: None)

    def test_paper_empty_resets_and_waits_for_two_complete_idle_reports(self):
        events = []
        frame = report()
        self.run_cancel(Cups(events), [report("stopped", "MediaEmpty", "00000001", "1000"),
                                      frame[:100], frame[100:], frame])
        self.assertEqual(events, ["guard", "claim", "guard", "reset", "query", "close"])

    def test_unknown_malformed_error_and_single_idle_never_release(self):
        for frames in [
            [report()], [b"broken</cmd>", report()], [report("stopped", "MediaEmpty")] * 4,
            [report(job="00000001")] * 4,
            [report(), report("processing"), report()],
            [report().replace(b"GetStatusResponse", b"CancelJobResponse")] * 4,
            [report().replace(b"</ivec:status>", b"</ivec:status><ivec:status>idle</ivec:status>")] * 4,
        ]:
            with self.subTest(frames=frames):
                events = []
                with self.assertRaises(RuntimeError): self.run_cancel(Cups(events), frames)
                self.assertEqual(events[-1], "close")

    def test_busy_before_or_after_claim_never_resets(self):
        for check in [1, 2]:
            events, cups = [], None
            cups = Cups(events)
            cups.fail_check = check
            with self.assertRaises(RuntimeError): self.run_cancel(cups, [])
            self.assertNotIn("reset", events)
            if check == 2: self.assertEqual(events[-1], "close")

    def test_target_change_never_resets(self):
        events = []
        cups = Cups(events)
        cups.change_target = True
        with self.assertRaises(RuntimeError): self.run_cancel(cups, [])
        self.assertNotIn("reset", events)
        self.assertEqual(events[-1], "close")

    def test_oversized_status_fails_closed(self):
        events = []
        with self.assertRaises(RuntimeError): self.run_cancel(Cups(events), [b"x" * 65537])
        self.assertEqual(events[-1], "close")

    def test_queue_target_only_matches_verified_profile(self):
        cups = adapter.Cups()
        cups.run = lambda *args: "device for Virtual: usb://Canon/G2010%20series?serial=synthetic&interface=1"
        self.assertEqual(cups.target("Virtual"), "synthetic")
        for uri in ["usb://Canon/G3010%20series?serial=x", "usb://Canon/G2010%20series?serial=x&interface=0",
                    "socket://printer", "usb://Canon/G2010%20series?serial=x&serial=y"]:
            cups.run = lambda *args: "device for Virtual: " + uri
            with self.assertRaises(RuntimeError): cups.target("Virtual")

    @unittest.skipIf(os.name == "nt", "Linux sysfs directory names contain colons")
    def test_device_selection_requires_exact_serial_and_printer_interface(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            device = root / "1-1"
            device.mkdir()
            for key, value in {"idVendor": "04a9", "idProduct": "183a", "serial": "synthetic",
                               "busnum": "1", "devnum": "4"}.items():
                (device / key).write_text(value)
            interface = device / "1-1:1.1"
            interface.mkdir()
            (interface / "bInterfaceClass").write_text("07")
            (interface / "bInterfaceSubClass").write_text("01")
            (interface / "ep_01").mkdir()
            (interface / "ep_82").mkdir()
            self.assertEqual(adapter.device_path("synthetic", root), "/dev/bus/usb/001/004")
            with self.assertRaises(RuntimeError): adapter.device_path("other", root)
            (interface / "bInterfaceClass").write_text("ff")
            with self.assertRaises(RuntimeError): adapter.device_path("synthetic", root)

    def test_unsafe_queue_is_rejected_before_hardware_access(self):
        for queue in ["", "../queue", "x\nother", "x;id"]:
            with self.assertRaises(ValueError): adapter.cancel(queue, None)


if __name__ == "__main__":
    unittest.main()
