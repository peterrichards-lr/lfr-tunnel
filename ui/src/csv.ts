// Builds a CSV in the browser and hands it to the user as a download.
//
// Every cell is quoted, with embedded quotes doubled (RFC 4180). A cell that a spreadsheet would
// read as a formula -- one starting with = + - @, a tab or a carriage return -- is prefixed with
// an apostrophe, so opening the export cannot run anything (CSV injection). Nothing exported
// today can start that way, but the rule belongs to the helper, not to whoever calls it next.
export function toCSV(rows: (string | number | null | undefined)[][]): string {
  return rows
    .map((row) =>
      row
        .map((cell) => {
          let s = cell === null || cell === undefined ? '' : String(cell);
          if (/^[=+\-@\t\r]/.test(s)) s = `'${s}`;
          return `"${s.replace(/"/g, '""')}"`;
        })
        .join(','),
    )
    .join('\n')
    .concat('\n');
}

export function downloadCSV(filename: string, csv: string): void {
  const url = URL.createObjectURL(
    new Blob([csv], { type: 'text/csv;charset=utf-8;' }),
  );
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
