import { writable } from 'svelte/store';

// liveRevision increments on every server-sent change. Pages subscribe and
// reload their data, so a change made in one tab (or by the assistant later)
// appears everywhere without polling.
export const liveRevision = writable(0);

// Bumped by the assistant workspace after a conversation is created or a
// message is sent, so the shell can refresh the recent dialog list.
export const conversationsRevision = writable(0);

// reminder is delivered by the planner when a deadline is approaching. It is a
// store rather than a page subscription because a reminder must reach the user
// wherever they are in the interface.
export interface ReminderNotice {
	task_id: number;
	title: string;
	kind: 'lead' | 'due' | 'overdue';
	due_at?: string;
	due_date?: string;
	fire_at: string;
}

export const reminderNotice = writable<ReminderNotice | null>(null);

export function dismissReminder(): void {
	reminderNotice.set(null);
}

export function bumpConversations(): void {
	conversationsRevision.update((value) => value + 1);
}

const eventTypes = [
	'task.created',
	'task.updated',
	'task.completed',
	'task.cancelled',
	'task.restored',
	'task.rescheduled',
	'inbox.captured',
	'inbox.converted',
	'inbox.dismissed',
	'insight.detected',
	'reminder',
	'plan.updated',
	'event.created',
	'event.updated',
	'event.deleted',
	'assistant.message',
	'pending_action.created',
	'pending_action.resolved'
];

let started = false;

export function startLive(): void {
	if (started || typeof window === 'undefined') return;
	// Headless screenshot tools wait for network idle; an open SSE stream never
	// becomes idle. This query flag is for visual verification only.
	if (new URLSearchParams(window.location.search).has('no_live')) return;
	started = true;

	const source = new EventSource('/api/v1/events');
	const bump = () => liveRevision.update((value) => value + 1);

	for (const type of eventTypes) {
		source.addEventListener(type, bump);
	}

	source.addEventListener('reminder', (event) => {
		try {
			const payload = JSON.parse((event as MessageEvent).data) as ReminderNotice;
			reminderNotice.set(payload);
			notifyDesktop(payload);
		} catch {
			// A malformed reminder is not worth breaking the stream for.
		}
	});

	// EventSource reconnects on its own and sends Last-Event-ID, so the server
	// replays anything missed while the connection was down.
	source.onerror = () => {};
}

// notifyDesktop shows a system notification when the tab is in the background and
// the user has granted permission. It is best effort: the in-page notice is the
// primary channel.
function notifyDesktop(notice: ReminderNotice): void {
	if (typeof window === 'undefined' || !('Notification' in window)) return;
	if (document.visibilityState === 'visible') return;
	if (Notification.permission !== 'granted') return;
	try {
		new Notification('LifeOS · напоминание', {
			body: notice.title,
			tag: `lifeos-task-${notice.task_id}`
		});
	} catch {
		// Some browsers refuse notifications outside a user gesture.
	}
}
