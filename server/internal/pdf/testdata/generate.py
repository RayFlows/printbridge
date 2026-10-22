"""Regenerate the synthetic fixture with ReportLab; never reads user documents."""
from pathlib import Path

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4, landscape
from reportlab.pdfgen.canvas import Canvas


target = Path(__file__).with_name("three-pages.pdf")
canvas = Canvas(str(target), pagesize=A4, invariant=1, pageCompression=1)
canvas.setTitle("PrintBridge synthetic preview fixture")
canvas.setAuthor("PrintBridge contributors")
for number, page_size in enumerate([A4, landscape(A4), A4], 1):
    width, height = page_size
    canvas.setPageSize(page_size)
    canvas.setFillColor(colors.HexColor("#087f73"))
    canvas.rect(42, height - 84, width - 84, 6, fill=1, stroke=0)
    canvas.setFillColor(colors.HexColor("#202628"))
    canvas.setFont("Helvetica-Bold", 26)
    canvas.drawString(42, height - 64, f"PrintBridge / Page {number}")
    canvas.setFont("Helvetica", 12)
    canvas.drawString(42, height - 120, "Synthetic PDF for upload and preview verification.")
    canvas.drawString(42, height - 144, "This document is never automatically sent to a printer.")
    canvas.setStrokeColor(colors.HexColor("#cbd3d6"))
    canvas.rect(42, 70, width - 84, height - 250, fill=0, stroke=1)
    canvas.setFont("Helvetica-Bold", 100)
    canvas.drawCentredString(width / 2, height / 2 - 40, str(number))
    canvas.setFont("Helvetica", 11)
    canvas.drawString(42, 42, "LANDSCAPE" if number == 2 else "PORTRAIT")
    canvas.drawRightString(width - 42, 42, f"{number} / 3")
    canvas.showPage()
canvas.save()
