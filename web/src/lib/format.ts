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

export function errorMessage(error: unknown): string {
	if (error instanceof Error) return error.message;
	return 'Не удалось выполнить запрос';
}
