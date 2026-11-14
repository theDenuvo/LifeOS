function escapeHtml(value: string): string {
	return value
		.replace(/&/g, '&amp;')
		.replace(/</g, '&lt;')
		.replace(/>/g, '&gt;')
		.replace(/"/g, '&quot;')
		.replace(/'/g, '&#39;');
}

function inline(value: string): string {
	let text = escapeHtml(value);
	text = text.replace(/`([^`]+)`/g, (_match, code: string) => `<code>${code}</code>`);
	text = text.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
	text = text.replace(/__([^_]+)__/g, '<strong>$1</strong>');
	text = text.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
	text = text.replace(
		/\[([^\]]+)\]\((https?:\/\/[^\s)]+|\/[^\s)]*)\)/g,
		'<a href="$2" target="_blank" rel="noreferrer noopener">$1</a>'
	);
	return text;
}

function isTableSeparator(line: string): boolean {
	const cells = line.trim().replace(/^\||\|$/g, '').split('|');
	return cells.length > 0 && cells.every((cell) => /^:?-{2,}:?$/.test(cell.trim()));
}

function tableCells(line: string): string[] {
	return line
		.trim()
		.replace(/^\||\|$/g, '')
		.split('|')
		.map((cell) => cell.trim());
}

// renderMarkdown is intentionally small and safe: all source text is escaped
// before a controlled set of block and inline tags is inserted.
export function renderMarkdown(source: string): string {
	const lines = source.replace(/\r\n?/g, '\n').split('\n');
	const output: string[] = [];
	let index = 0;

	while (index < lines.length) {
		const line = lines[index] ?? '';

		if (line.trim().startsWith('```')) {
			const language = line.trim().slice(3).trim().replace(/[^a-zA-Z0-9-]/g, '');
			index++;
			const code: string[] = [];
			while (index < lines.length) {
				const current = lines[index] ?? '';
				if (current.trim().startsWith('```')) break;
				code.push(current);
				index++;
			}
			index++;
			output.push(
				`<pre><code${language ? ` class="language-${language}"` : ''}>${escapeHtml(code.join('\n'))}</code></pre>`
			);
			continue;
		}

		const heading = /^(#{1,6})\s+(.+)$/.exec(line);
		if (heading) {
			const level = heading[1]!.length;
			output.push(`<h${level}>${inline(heading[2] ?? '')}</h${level}>`);
			index++;
			continue;
		}

		if (/^\s*(---+|\*\*\*+)\s*$/.test(line)) {
			output.push('<hr>');
			index++;
			continue;
		}

		if (line.includes('|') && index + 1 < lines.length && isTableSeparator(lines[index + 1] ?? '')) {
			const header = tableCells(line);
			index += 2;
			const rows: string[][] = [];
			while (index < lines.length) {
				const current = lines[index] ?? '';
				if (!current.includes('|') || current.trim() === '') break;
				rows.push(tableCells(current));
				index++;
			}
			const head = header.map((cell) => `<th>${inline(cell)}</th>`).join('');
			const body = rows
				.map(
					(row) =>
						`<tr>${header.map((_, cellIndex) => `<td>${inline(row[cellIndex] ?? '')}</td>`).join('')}</tr>`
				)
				.join('');
			output.push(`<div class="md-table"><table><thead><tr>${head}</tr></thead><tbody>${body}</tbody></table></div>`);
			continue;
		}

		if (/^\s*>\s?/.test(line)) {
			const quote: string[] = [];
			while (index < lines.length) {
				const current = lines[index] ?? '';
				if (!/^\s*>\s?/.test(current)) break;
				quote.push(current.replace(/^\s*>\s?/, ''));
				index++;
			}
			output.push(`<blockquote>${quote.map((part) => inline(part)).join('<br>')}</blockquote>`);
			continue;
		}

		const listMatch = /^\s*([-*]|\d+\.)\s+(.+)$/.exec(line);
		if (listMatch) {
			const ordered = /\d+\./.test(listMatch[1] ?? '');
			const items: string[] = [];
			while (index < lines.length) {
				const item = /^\s*([-*]|\d+\.)\s+(.+)$/.exec(lines[index] ?? '');
				if (!item || /\d+\./.test(item[1] ?? '') !== ordered) break;
				items.push(`<li>${inline(item[2] ?? '')}</li>`);
				index++;
			}
			output.push(ordered ? `<ol>${items.join('')}</ol>` : `<ul>${items.join('')}</ul>`);
			continue;
		}

		if (line.trim() === '') {
			index++;
			continue;
		}

		const paragraph: string[] = [];
		while (index < lines.length) {
			const current = lines[index] ?? '';
			if (current.trim() === '') break;
			if (
				current.trim().startsWith('```') ||
				/^(#{1,6})\s+/.test(current) ||
				/^\s*([-*]|\d+\.)\s+/.test(current) ||
				/^\s*>\s?/.test(current)
			) {
				break;
			}
			paragraph.push(current);
			index++;
		}
		output.push(`<p>${paragraph.map((part) => inline(part)).join('<br>')}</p>`);
	}

	return output.join('');
}
