import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("installer", Path(__file__).parents[1] / "install-canon-monitor.py")
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)

PPD = b'*PPD-Adobe: "4.3"\n*Manufacturer: "Canon"\n*cupsFilter: "application/vnd.cups-raster 100 rastertogutenprint"\n'


class FakeCups:
    def __init__(self):
        self.active, self.raced, self.accepting = False, False, True
        self.monitor, self.calls, self.failed = "none", [], False

    def idle(self, queue):
        if self.active:
            raise RuntimeError("unfinished jobs")

    def attributes(self, queue):
        return self.monitor, self.accepting

    def reject(self, queue):
        self.calls.append("reject")
        self.accepting = False
        if self.raced:
            self.active = True

    def accept(self, queue):
        self.calls.append("accept")
        self.accepting = True

    def configure(self, queue, ppd, monitor):
        self.calls.append((ppd.read_bytes(), monitor))
        if self.failed and monitor != "none":
            raise RuntimeError("activation failed")
        self.monitor = monitor

    def run(self, *args):
        return "printbridge-canon-monitor 1\n"


class CupsStateTests(unittest.TestCase):
    def probe(self, state, jobs=""):
        class ProbeCups(installer.Cups):
            def run(self, *args):
                if args[0] == "lpstat":
                    return jobs
                return "" if state is None else "    printer-state (enum) = " + state + "\n"
        return ProbeCups()

    def test_processing_queue_refuses_even_when_canceled_job_is_not_listed(self):
        with self.assertRaisesRegex(RuntimeError, "processing"):
            self.probe("processing").idle("virtual")

    def test_unknown_or_missing_printer_state_refuses(self):
        for state in [None, "unknown"]:
            with self.subTest(state=state), self.assertRaises(RuntimeError):
                self.probe(state).idle("virtual")

    def test_idle_and_stopped_queues_without_jobs_allow_installation(self):
        for state in ["idle", "stopped"]:
            with self.subTest(state=state):
                self.probe(state).idle("virtual")


class InstallerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        (self.root / "ppd").mkdir()
        (self.root / "ppd" / "virtual.ppd").write_bytes(PPD)
        self.binary = self.root / "candidate"
        self.binary.write_bytes(b"synthetic executable")
        self.cups = FakeCups()
        self.installer = installer.Installer(self.cups, self.root / "ppd",
                                              self.root / "monitor", self.root / "backups")

    def test_unfinished_job_refuses_before_any_change(self):
        self.cups.active = True
        with self.assertRaisesRegex(RuntimeError, "unfinished"):
            self.installer.install("virtual", self.binary)
        self.assertEqual(self.cups.calls, [])
        self.assertFalse((self.root / "monitor").exists())
        self.assertFalse((self.root / "backups").exists())

    def test_racing_submission_restores_acceptance_without_changing_ppd(self):
        self.cups.raced = True
        with self.assertRaises(RuntimeError):
            self.installer.install("virtual", self.binary)
        self.assertEqual(self.cups.calls, ["reject", "accept"])
        self.assertFalse((self.root / "monitor").exists())

    def test_install_repeat_and_rollback_preserve_driver_and_previous_monitor(self):
        backup = self.installer.install("virtual", self.binary)
        self.assertEqual((backup / "original.ppd").read_bytes(), PPD)
        modified = (backup / "monitor.ppd").read_bytes()
        self.assertTrue(modified.startswith(PPD))
        self.assertEqual(modified.count(b"cupsPortMonitor"), 1)
        self.assertTrue(self.cups.accepting)
        calls = len(self.cups.calls)
        self.assertIsNone(self.installer.install("virtual", self.binary))
        self.assertEqual(len(self.cups.calls), calls)
        self.installer.rollback(backup)
        self.assertEqual(self.cups.monitor, "none")
        self.assertEqual(self.cups.calls[-2], (PPD, "none"))
        self.assertTrue(self.cups.accepting)

    def test_failure_restores_original_and_a_rejecting_queue_stays_rejecting(self):
        self.cups.failed = True
        self.cups.accepting = False
        with self.assertRaisesRegex(RuntimeError, "activation failed"):
            self.installer.install("virtual", self.binary)
        self.assertEqual(self.cups.monitor, "none")
        self.assertFalse(self.cups.accepting)
        self.assertEqual(self.cups.calls[-1], (PPD, "none"))

    def test_rollback_refuses_active_jobs_and_newer_monitor_settings(self):
        backup = self.installer.install("virtual", self.binary)
        self.cups.active = True
        with self.assertRaisesRegex(RuntimeError, "unfinished"):
            self.installer.rollback(backup)
        self.cups.active = False
        self.cups.monitor = "another"
        with self.assertRaisesRegex(RuntimeError, "changed"):
            self.installer.rollback(backup)
        self.assertEqual(self.cups.monitor, "another")

    def test_rollback_preserves_later_ppd_edits(self):
        backup = self.installer.install("virtual", self.binary)
        (self.root / "ppd" / "virtual.ppd").write_bytes(PPD + b'*DefaultPageSize: A5\n')
        with self.assertRaisesRegex(RuntimeError, "PPD changed"):
            self.installer.rollback(backup)


if __name__ == "__main__":
    unittest.main()
