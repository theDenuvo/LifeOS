import type { Priority, Status } from './api';

const dateFormatter = new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'short' });
const weekdayFormatter = new Intl.DateTimeFormat('ru-RU', { weekday: 'long', day: 'numeric', month: 'long' });

export function formatDate(value: string | null): string {
	if (!value) return '—';
	const date = new Date(value.length === 10 ? `${value}T00:00:00` : value);
	if (Number.isNaN(date.getTime())) return '—';
	return dateFormatter.format(date);
}

export function formatLongDate(date: Date): string {
	const text = weekdayFormatter.format(date);
	return text.charAt(0).toUpperCase() + text.slice(1);
}

export function formatTime(value: string | null): string {
	if (!value) return '—';
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return '—';
	return date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
}

export function priorityLabel(priority: Priority): string {
	switch (priority) {
		case 'high':
			return 'Высокий';
		case 'medium':
			return 'Средний';
		default:
			return 'Низкий';
	}
}

export function statusLabel(status: Status): string {
	switch (status) {
		case 'completed':
			return 'Выполнена';
		case 'cancelled':
			return 'Отменена';
		default:
			return 'Активна';
	}
}

export function todayISO(): string {
	const now = new Date();
	const local = new Date(now.getTime() - now.getTimezoneOffset() * 60_000);
	return local.toISOString().slice(0, 10);
}

/**
 * calendarDateInZone renders an instant as YYYY-MM-DD in the given IANA zone.
 *
 * A task with an exact deadline (due_at) has no stored calendar date, so the
 * date input needs the deadline's local day — computed in the task's own
 * timezone, not in the browser's.
 */
export function calendarDateInZone(value: string, timeZone: string): string {
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return '';
	try {
		const parts = new Intl.DateTimeFormat('en-CA', {
			timeZone,
			year: 'numeric',
			month: '2-digit',
			day: '2-digit'
		}).formatToParts(date);
		const get = (type: string) => parts.find((part) => part.type === type)?.value ?? '';
		const formatted = `${get('year')}-${get('month')}-${get('day')}`;
		return /^\d{4}-\d{2}-\d{2}$/.test(formatted) ? formatted : '';
	} catch {
		// An unknown zone falls back to the browser's local date.
		const local = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
		return local.toISOString().slice(0, 10);
	}
}

export function errorMessage(error: unknown): string {
	if (error instanceof TypeError) {
		return 'Нет связи с сервером. Проверьте соединение и повторите.';
	}
	if (error instanceof Error) {
		const message = error.message.trim();
		const lower = message.toLowerCase();
		if (
			lower === 'network error' ||
			lower.includes('failed to fetch') ||
			lower.includes('networkerror') ||
			lower.includes('load failed')
		) {
			return 'Нет связи с сервером. Проверьте соединение и повторите.';
		}
		return message;
	}
	return 'Не удалось выполнить запрос';
}

/** True when the failure means the transport died rather than the server refusing. */
export function isConnectionError(error: unknown): boolean {
	if (error instanceof TypeError) return true;
	const message = error instanceof Error ? error.message.toLowerCase() : '';
	return (
		message.includes('network error') ||
		message.includes('failed to fetch') ||
		message.includes('networkerror') ||
		message.includes('load failed') ||
		message.includes('err_connection')
	);
}
