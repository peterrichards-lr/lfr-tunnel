import { useEffect } from 'react';
import { Chart } from 'chart.js';

// Redraws every chart at the size it will have on paper, for as long as the page prints (#2430).
//
// Chart.js draws once, at screen size. Print CSS can only scale that bitmap, so an export carried
// shrunken copies of the screen: ~5px axis text on a wide chart, and cards half empty where a
// wide bitmap was scaled down inside a fixed-height container. The browser does not resize a
// chart for print on its own, so it is done here: on beforeprint, resize each chart to its
// printed width; on afterprint, hand it back to its container.
//
// The printed width cannot be measured at beforeprint -- the print layout does not exist yet --
// so it is derived from the page. The numbers below are index.css's @page rules, and Portal V1
// (dashboard.js, sizeChartsForPrint) uses the same ones; change all three together.

// A4 content width in CSS px (96/in) once the @page margins are taken off.
const PRINT_CONTENT_WIDTH_PX = {
  portrait: 680, // 210mm - 2 x 15mm
  wide: 971, // 297mm - 2 x 20mm, the `wide` landscape page
};

// Paper is read closer than a screen; a 1x bitmap prints soft.
const PRINT_PIXEL_RATIO = 2;

// The print stylesheet lays .auto-grid-lg out in exactly two columns, so a chart's column width
// is known rather than guessed from an auto-fit track list.
const TWO_COLUMN_GRID = 'auto-grid-lg';

type Saved = { ratio: number | undefined; width: number; height: number };
const saved = new WeakMap<Chart, Saved>();

/** The width a chart will print at, or null when it is not on the page at all. */
export function printWidthFor(canvas: HTMLCanvasElement): number | null {
  const container = canvas.parentElement;
  const card = container?.closest<HTMLElement>('.card');
  if (!container || !card) return null;
  const cardBox = card.getBoundingClientRect();
  if (cardBox.width === 0) return null;

  const page = card.closest('.print-wide') ? 'wide' : 'portrait';
  let column = PRINT_CONTENT_WIDTH_PX[page];
  const grid = card.parentElement;
  if (grid?.classList.contains(TWO_COLUMN_GRID)) {
    const gap = parseFloat(getComputedStyle(grid).columnGap) || 0;
    column = (column - gap) / 2;
  }
  // The card's padding and border are the same on paper as on screen, so the difference
  // measured now is the difference that will apply then.
  const inset = cardBox.width - container.getBoundingClientRect().width;
  return Math.floor(column - inset);
}

export function sizeChartsForPrint() {
  for (const chart of Object.values(Chart.instances)) {
    const width = printWidthFor(chart.canvas);
    if (width === null || width <= 0) continue;
    const height = chart.canvas.parentElement?.clientHeight || chart.height;
    if (!saved.has(chart)) {
      saved.set(chart, {
        ratio: chart.options.devicePixelRatio,
        width: chart.width,
        height: chart.height,
      });
    }
    chart.options.devicePixelRatio = PRINT_PIXEL_RATIO;
    // Chart.js defers a resize while an animation runs, to its next frame -- after the browser
    // has already laid out the print. A chart still animating in would print at screen size.
    chart.stop();
    chart.resize(width, height);
  }
}

export function restoreChartsAfterPrint() {
  for (const chart of Object.values(Chart.instances)) {
    const s = saved.get(chart);
    if (!s) continue;
    chart.options.devicePixelRatio = s.ratio;
    saved.delete(chart);
    // Back to the size it had, not to whatever its container measures now: the printed canvas
    // has stretched a grid cell, so the container would hand the print width back.
    chart.stop();
    chart.resize(s.width, s.height);
  }
}

export function usePrintSizedCharts() {
  useEffect(() => {
    window.addEventListener('beforeprint', sizeChartsForPrint);
    window.addEventListener('afterprint', restoreChartsAfterPrint);
    return () => {
      window.removeEventListener('beforeprint', sizeChartsForPrint);
      window.removeEventListener('afterprint', restoreChartsAfterPrint);
    };
  }, []);
}
