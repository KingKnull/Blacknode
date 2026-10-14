export type TerminalSearchMatch = {
  row: number;
  column: number;
  length: number;
};

type SearchCell = {
  getChars(): string;
  getWidth(): number;
};

type SearchLine = {
  length: number;
  getCell(column: number): SearchCell | undefined;
};

type SearchBuffer = {
  length: number;
  getLine(row: number): SearchLine | undefined;
};

export function findTerminalMatches(
  buffer: SearchBuffer | undefined,
  query: string,
): TerminalSearchMatch[] {
  if (!buffer || !query) return [];
  const needle = query.toLowerCase();
  const matches: TerminalSearchMatch[] = [];

  for (let row = 0; row < buffer.length; row++) {
    const line = buffer.getLine(row);
    if (!line) continue;

    let text = "";
    const columns: number[] = [];
    for (let column = 0; column < line.length; column++) {
      const cell = line.getCell(column);
      const chars = cell?.getChars() || (cell?.getWidth() === 0 ? "" : " ");
      const folded = chars.toLowerCase();
      text += folded;
      for (let offset = 0; offset < folded.length; offset++) columns.push(column);
    }

    let offset = 0;
    while ((offset = text.indexOf(needle, offset)) !== -1) {
      const startColumn = columns[offset];
      const endColumn = columns[offset + needle.length - 1];
      if (startColumn !== undefined && endColumn !== undefined) {
        matches.push({
          row,
          column: startColumn,
          length: endColumn - startColumn + 1,
        });
      }
      offset++;
    }
  }

  return matches;
}
