# Notes: site yield analysis, 118 S First Street, Bloomingdale IL

Task `fp-118-first-st`.
Deliverable: `C:\Users\fpres\OneDrive - SIQstack\Clients\Franklin-Properties\118-S-1st-St-Site-Yield-Analysis.pdf`.
Source: `118-s-1st-st-site-yield-analysis.html` in this folder, a single self-contained file.

## How to rebuild

```
"C:\Program Files\Google\Chrome\Application\chrome.exe" --headless --disable-gpu --no-pdf-header-footer \
  --print-to-pdf="<output.pdf>" "file:///<absolute path>/118-s-1st-st-site-yield-analysis.html"
```

No dependencies, no PDF library.
Output at time of writing: 37 pages, 943 KB, US Letter portrait.

## Sources of fact

`data/fp-118-first-st/facts.md` was the only permitted fact base, together with three instructions the Overlord issued mid-task:

1. A second independent parcel polygon computation, 12,334 sq ft with 96.34 ft frontage and 127.99 to 128.26 ft depth, corroborating the DuPage cadastral figure to within 0.1 percent.
   The zoning record was corrected from OT to R-2 on the source project, original value preserved.
2. `facts.md` section 17, appended after the brief was issued, which overrides sections 12 and 13 where they conflict.
3. `facts.md` section 18, the client-directed scope change: the report carries **two fully dimensioned site plan exhibits at equal weight**, with a side by side comparison and **no recommendation** between them.

No number, standard, citation, date, cost, rent, absorption rate or duration appears in the report that is not traceable to one of those.
Where a fact was missing, the report says "not established" and carries it as an open item.

## What section 17 changed, and where it landed in the report

| Change | Effect on the report |
|---|---|
| FAR ceiling is 0.39, not 0.40, because 11-18-3C1 says "in no event" | The whole "0.40 from R-3 and R-4 precedent" argument was removed. Section 4.6 now quotes the cap; Scenario C recomputes at 0.39 |
| New constraint: 25 percent parcel coverage, 3,080 sq ft | Added to Section 4.6 and checked in Scenario C (19.5 percent) and Scenario F |
| Density reduction 20 percent compounded, 2,268 sq ft per unit | Section 4.6, and the basis of the 5-unit hard ceiling |
| Hard legal ceiling is 5 units, not 4 | Scenario D added, testing the ceiling in townhome form and finding it stops at four on unit width |
| Scenario C geometry: 1,201 sq ft per unit, 31.5 ft depth, 54.6 ft rear yard | Recomputed everywhere, and the site plan was redrawn to those dimensions |
| 10-unit gap restated at 1.84 times, not 2.27 times | Scenario E uses the reduced-density test as the strongest form of the argument |
| Two real durations, both eighteen months, 11-18-4C1 and 11-18-6 | Section 8.5, described explicitly as post-approval validity periods, not processing times |
| Open items 14 to 16 | Section 10. OI-14 has its own callout in Scenario C and again on the open items sheet, per instruction |

## What section 18 changed

| Change | Where it landed |
|---|---|
| Two exhibits at equal weight, no recommendation | Cover, executive summary, Section 6 ("The two concepts"), Exhibits 1 and 2, and the new Section 7 comparison. Every "recommended" label was removed from Scenario C and from the yield summary |
| Built form does not raise the unit ceiling | New **Section 5.1**, placed before the scenarios, plus the second paragraph of the executive summary. The point that four units at 19.04 ft is 76.16 ft exactly, with no gaps to remove, is the argument that disposes of the misconception |
| Hard finding: five two-bedroom apartments cannot be delivered | Scenario F. They need FAR 0.459, or 0.418 crediting the full 500 sq ft garage exclusion, against the 0.39 ceiling. Five one-bedroom units at about 680 sq ft do clear. Stated as a finding, not a caution |
| The four two-bedroom variant | Scenario F. It fits at FAR 0.372 but forfeits the "five or more dwelling units" qualification at 11-7H-2, which is the whole strategic point of the apartment form. Presented as the developer's tension to resolve |
| Apartment form lowers OI-14 risk | Cross referenced in Scenario F, in the Exhibit 2 plan notes, in the OI-14 callout on the open items sheet, and in the Section 7 comparison table |
| Exhibit 2 geometry from 18.5 | 42 + 40 + 46.1 = 128.1 ft and 18.08 + 60 + 18.08 = 96.16 ft, both closure-checked on the sheet. 60 x 40 footprint, 2,400 sq ft, 19.5 percent coverage |
| Apartment parking, 18.4 | 11 spaces: 5 garage bays at grade plus 6 surface stalls, one of them guest. The "not less than 1 per 4 units" reading that would give 12 is flagged, not resolved |
| 11-13-4 F11 landscaping and F2 pavement | Engage for Exhibit 2's six surface stalls and are drawn and noted. They do not engage the same way in Exhibit 1 |

**Superseded figures.** An earlier draft of Scenario F used about 840 sq ft per unit, from a garage-only deduction. Section 18.3 adds circulation, giving about 680 sq ft per unit, and that is the figure the report now carries throughout.

## Design and production decisions

- **Explicit paged layout.** Each page is a fixed 8.5 x 11 in `.sheet` with `@page { margin: 0 }` and padding inside.
  Chrome does not implement CSS margin boxes, so `@bottom-center { content: counter(page) }` is unavailable; page numbers are stamped by a small inline script after layout, and the running footer carries the address and PIN on every sheet.
- **Overflow was measured, not eyeballed.** Fixed-height sheets with `overflow: hidden` clip silently, which is the one failure mode that would have shipped a truncated report. A throwaway probe page injected a script that reported, for each sheet, the gap between the bottom of its last child and the bottom of its content box. That drove the typography pass and the sheet splits until every sheet cleared.
  If the report is edited, re-run that check rather than trusting the page count: the page count stays the same whether or not content is being clipped.
- **Both site plans are drawn vector geometry**, inline SVG, viewBox in feet so every coordinate in the markup is a real dimension. No aerial imagery is traced or embedded.
  Exhibit 1 carries the four aprons, four attached garages with door symbols, the single 24 ft curb cut, the 24 ft shared aisle, the guest space, the wood picket fence per 11-11-8A and the detention reserve; closure 42 + 31.5 + 54.6 = 128.1 ft and 10 + 76.16 + 10 = 96.16 ft.
  Exhibit 2 carries the 60 x 40 building, five garage bays at grade with door symbols, six surface stalls including the guest space, perimeter landscaping, the same curb cut and aisle, the fence and the detention reserve; closure 42 + 40 + 46.1 = 128.1 ft and 18.08 + 60 + 18.08 = 96.16 ft.
- **One accent colour** (`#1a4a5e`) and a small grey ramp. The drawing reads in line weights rather than colour.
- No em dash characters anywhere, per the brief.

## Judgements made, and flagged as judgements in the report

- **No recommendation between the two concepts**, per the client's direction. The report states the trade off and stops. Scenario D still explains why the townhome form stops at four units at 15.23 ft width, but that is a statement about the form, not a preference between exhibits.
- **The multiple-family parking row was used, not the single-family-attached row.** The printed single-family-attached row would total 4.5 spaces per unit, which is anomalous on its face. The choice is stated in the report and the question is carried as OI-07.
- **Both concepts are drawn to the same standard.** Exhibit 1 and Exhibit 2 each carry a scaled SVG plan, north arrow, graphic scale, legend, dimension table, plan notes, closure checks and the same "not a survey" label. Neither sheet is subordinate to the other in layout or in weight.

## Things deliberately not done

- No processing durations anywhere. 11-3-4 could not be retrieved, so notice period and hearing lead time are "not established" (OI-04).
- No tax figure. The parcel is exempt at $0 assessed value and no rate is established (OI-12).
- The R-2 FAR garage exclusion was not resolved in the applicant's favour. Scenario C's arithmetic does not depend on it (OI-08).
- No attempt was made to reach the PrecisionDocs project.

---

# Notes: plain-English summary, 118 S First Street

Task `fp-118-rewrite`.
Deliverable: `C:\Users\fpres\OneDrive - SIQstack\Clients\Franklin-Properties\118-S-1st-St-Site-Yield-Summary.pdf`.
Source: `118-s-1st-st-site-yield-summary.html` in this folder, a single self-contained file.
The 37 page engineering version and its PDF are unchanged and stay where they are.

## Why it exists

The client read the detailed report and could not use it: "I can't understand the site yield, make it laymen's terms",
"make it simple, easy to read, way less dense and more high level", "that a property manager not engineer can understand",
"I also can't understand site plans".

Same facts, 14 pages instead of 37, written for a property manager or developer principal rather than an engineer.

## How to rebuild

```
"C:\Program Files\Google\Chrome\Application\chrome.exe" --headless --disable-gpu --no-pdf-header-footer \
  --print-to-pdf="<output.pdf>" "file:///<absolute path>/118-s-1st-st-site-yield-summary.html"
```

Output at time of writing: 14 pages, 1.93 MB, US Letter portrait.

## Sources of fact

`data/fp-118-rewrite/facts.md`, the same fact base as the detailed report, remained the only permitted source.
No number, standard, citation, date, cost, rent or duration appears that is not traceable to it.
Every figure in the body was audited against the fact base after the last edit.
Two figures are stated in rounded plain-English form and are flagged here so they are not mistaken for new facts:
FAR 0.459 and 0.418 appear as "about 46 percent" and "about 42 percent"; 1,201 sq ft appears as "about 1,200 sq ft"
on the cover and answer page, alongside the precise figure everywhere else.

## Structure

Cover, the answer, how the limit works, how big the homes can be, option 1 over two pages, option 2 over two pages,
side by side plus the site in plain words, the deciding question, what to do in order, what could still change the
answer, then the two dimensioned engineering sheets as Appendix A and Appendix B.

The land-per-home framing carries the yield explanation: the town caps how little land sits under each home, not how
many homes you build. The divisions are shown as a table a reader can follow with a calculator, and the two ways a
developer expects to beat the rule, stacking and closing the gaps between units, are disposed of explicitly.

## The drawings

- **Depth strip diagram**, one per option: a horizontal bar from the street to the back of the lot, segments sized in
  proportion and labelled in plain words. This is the diagram a reader who cannot read a site plan can still read.
- **Real-imagery aerial exhibits**, `assets/aerial-townhomes.svg` and `assets/aerial-apartments.svg`, dropped in at full
  width, inlined verbatim. Not redrawn, not re-annotated, not scaled off. The Esri attribution is repeated in the caption
  as well as in the SVG footer.
- **Marketing renders**, `assets/render-townhomes.jpg` and `assets/render-apartments.jpg`, at the top of each option, each
  carrying a disclaimer that states plainly that they are illustrative, not architecture, not approved, not a depiction of
  any real building, and generated from a written description with no site imagery used.
- The dimensioned engineering plans moved to the appendix, each with a note saying they are for the architect and civil
  engineer rather than the reader.

## The detention correction

The client was right. In the detailed report the underground tank was drawn as a solid hatched block filling the rear
yard, which read as land the owner could not use. Underground detention is a buried tank: the ground above it stays yard
and it displaces no surface area.

Both appendix exhibits now draw it as a ghosted dashed outline at reduced weight sitting behind the surface information,
relabelled "underground stormwater tank, below grade. Yard above remains open space", with a plan note stating that it
occupies no surface area, does not reduce open space or usable yard, and sits within the area already required to remain
open. The legend entry was rewritten to match. The caution that hydrologic soil group C soils and a 2.5 ft water table
are adverse for a buried structure, and that a geotechnical investigation is required, was correct and is kept.

## Production

- The appendix sheets are the two exhibits from the detailed report, carried over rather than redrawn, with the detention
  fix, the "for your engineer" note, and cross references rewritten from open item codes to plain pointers.
- **Overflow was measured, not eyeballed.** Fixed-height sheets with `overflow: hidden` clip silently. A probe copy of the
  built file reports, per sheet, the gap between the bottom of its last child and the bottom of its content box. Every
  sheet clears with room to spare. Re-run that check after any edit; the page count stays at 14 whether or not content is
  being clipped.
- Body type is set larger and looser than the detailed report, line length is held to about 5.7 inches, and no page is
  filled edge to edge.
- One accent colour, `#1a4a5e`. No em dash characters anywhere; the build asserts on them.
