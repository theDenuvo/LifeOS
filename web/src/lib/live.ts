import { writable } from 'svelte/store';

// liveRevision increments on every server-sent change. Pages subscribe and
// reload their data, so a change made in one tab (or by the assistant later)
// appears everywhere without polling.
export const liveRevision = writable(0);

// Bumped by the assistant workspace after a conversation is created or a
// message is sent, so the shell can refresh the recent dialog list.
export const conversationsRevision = writable(0);

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

	// EventSource reconnects on its own and sends Last-Event-ID, so the server
	// replays anything missed while the connection was down.
	source.onerror = () => {};
}
