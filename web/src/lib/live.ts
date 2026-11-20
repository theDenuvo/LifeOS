import { writable } from 'svelte/store';
import { ackReminder, getDueReminders } from './api';

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
	/** Present for anything that came from the durable endpoint. */
	reminder_id?: number;
	task_id: number;
	title: string;
	kind: 'lead' | 'due' | 'overdue';
	due_at?: string;
	due_date?: string;
	fire_at: string;
}

// shownReminders remembers which reminders this tab already displayed, so a
// re-render, a second live event or a repeated poll cannot produce a duplicate
// toast. The server is still the source of truth: an acknowledged reminder is not
// offered again after a reload.
const shownReminders = new Set<number>();

/**
 * deliverReminder shows a reminder once and remembers that it was shown.
 *
 * The reminder is acknowledged on the server only after it is on screen, which is
 * what makes "delivered" mean "the user saw it" instead of "we published an
 * event".
 */
function deliverReminder(notice: ReminderNotice): void {
	const id = notice.reminder_id;
	if (id !== undefined) {
		if (shownReminders.has(id)) return;
		shownReminders.add(id);
	}
	reminderNotice.set(notice);
	notifyDesktop(notice);
	if (id !== undefined) {
		void ackReminder(id).catch(() => {
			// A failed acknowledgement is not a lost reminder: it stays pending on
			// the server and comes back on the next load.
			shownReminders.delete(id);
		});
	}
}

/**
 * catchUpReminders fetches the reminders that fired while this tab was closed.
 *
 * Live events are best effort — nobody may be connected when the moment comes —
 * so the durable list is what actually guarantees that a reminder is not lost.
 */
async function catchUpReminders(): Promise<void> {
	if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
	try {
		const payload = await getDueReminders();
		for (const reminder of payload.data) {
			deliverReminder({
				reminder_id: reminder.reminder_id,
				task_id: reminder.task_id,
				title: reminder.title,
				kind: reminder.kind,
				due_at: reminder.due_at,
				due_date: reminder.due_date,
				fire_at: reminder.fire_at
			});
		}
	} catch {
		// Offline or the server is restarting: the next load tries again.
	}
}

export const reminderNotice = writable<ReminderNotice | null>(null);

export function dismissReminder(): void {
	reminderNotice.set(null);
}

// outreach is LifeOS starting the dialogue itself (roadmap stage 5). Like a
// reminder it must reach the user wherever they are, so it lives here instead
// of one page's subscription.
export interface OutreachNotice {
	id: number;
	conversation_id: number;
	title: string;
	severity: 'critical' | 'warning' | 'info';
}

export const outreachNotice = writable<OutreachNotice | null>(null);

export function dismissOutreachNotice(): void {
	outreachNotice.set(null);
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
	'outreach.created',
	'reminder',
	'plan.updated',
	'event.created',
	'event.updated',
	'event.deleted',
	'assistant.message',
	'assistant.workspace',
	'pending_action.created',
	'pending_action.resolved',
	// Goals and memory are edited from the assistant as well as the workspace;
	// without these the pages that subscribe to liveRevision never refresh.
	'goal.created',
	'goal.updated',
	'goal.deleted',
	'memory.created',
	'memory.updated',
	'memory.deleted',
	'memory.conflict.detected',
	'memory.conflict.resolved',
	// Автоматизации меняются и ассистентом, и планировщиком; без этих событий
	// страницы, подписанные на liveRevision, показывали бы устаревшее состояние.
	'automation.created',
	'automation.updated',
	'automation.deleted',
	'automation.run.started',
	'automation.run.finished',
	'skill.created',
	'skill.updated',
	'skill.deleted',
	'skill.proposal.created',
	'skill.proposal.resolved',
	'skill.feedback',
	'skill.instructions.updated',
	// Sources and links change what an entry shows even though the entry
	// itself is not edited, so they need the same refresh.
	'memory.source.added',
	'memory.linked',
	'memory.unlinked',
	// Captures can be edited without being created, converted or dismissed.
	'inbox.updated',
	// The server sends resync when a burst outran the replay buffer: some events
	// are gone, so every page must re-read its state instead of trusting the
	// stream to be complete.
	'resync'
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

	source.addEventListener('assistant.workspace', () => bumpConversations());

	source.addEventListener('resync', () => {
		// A gap the server could not repair means this tab may be showing stale
		// state. Reloading from the API is what closes it.
		bump();
		void catchUpReminders();
	});

	source.addEventListener('reminder', (event) => {
		try {
			const payload = JSON.parse((event as MessageEvent).data) as ReminderNotice;
			deliverReminder(payload);
		} catch {
			// A malformed reminder is not worth breaking the stream for.
		}
	});

	// An outreach is LifeOS speaking first. The toast links into the main
	// chat, where the full reason and the proposal wait as an assistant
	// message; the chat reloads it through the same liveRevision bump.
	source.addEventListener('outreach.created', (event) => {
		try {
			const payload = JSON.parse((event as MessageEvent).data) as {
				id: number;
				conversation_id: number;
				title: string;
				severity: string;
			};
			outreachNotice.set({
				id: payload.id,
				conversation_id: payload.conversation_id,
				title: payload.title,
				severity: payload.severity === 'critical' ? 'critical' : payload.severity === 'info' ? 'info' : 'warning'
			});
		} catch {
			// A malformed outreach event is not worth breaking the stream for.
		}
	});

	// EventSource reconnects on its own and sends Last-Event-ID, so the server
	// replays anything missed while the connection was down.
	source.onerror = () => {};

	// The live stream is best effort; the stored list is what makes delivery
	// reliable. Ask for it on start, on every reconnect and when the tab comes
	// back to the foreground.
	void catchUpReminders();
	source.addEventListener('open', () => {
		void catchUpReminders();
	});
	document.addEventListener('visibilitychange', () => {
		if (document.visibilityState === 'visible') void catchUpReminders();
	});
	window.addEventListener('focus', () => {
		void catchUpReminders();
	});
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
