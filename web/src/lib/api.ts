export type Priority = 'low' | 'medium' | 'high';
export type Status = 'open' | 'completed' | 'cancelled';

export interface Task {
	id: number;
	title: string;
	note: string | null;
	priority: Priority;
	status: Status;
	planned_date: string | null;
	due_date: string | null;
	due_at: string | null;
	timezone: string;
	tags: string[];
	created_at: string;
	updated_at: string;
	completed_at: string | null;
	cancelled_at: string | null;
	overdue: boolean;
	version: number;
}

export interface TaskList {
	data: Task[];
	meta: { limit: number; offset: number; total: number };
}

export interface InboxItem {
	id: number;
	text: string;
	meta: Record<string, unknown> | null;
	created_at: string;
	processed_at: string | null;
	converted_task_id: number | null;
}

export interface InboxList {
	data: InboxItem[];
	meta: { limit: number; offset: number; total: number };
}

export interface SummaryItem {
	id: number;
	title: string;
	time: string;
	priority: Priority;
	days_overdue?: number;
}

export interface Summary {
	today: SummaryItem[];
	overdue: SummaryItem[];
	high_priority: SummaryItem[];
	stats: {
		active_total: number;
		overdue_count: number;
		high_priority_count: number;
		completed_today: number;
		inbox_count: number;
	};
	focus_task: SummaryItem | null;
}

export interface ProblemDetails {
	type?: string;
	title?: string;
	status?: number;
	detail?: string;
}

export class ApiError extends Error {
	status: number;
	problem: ProblemDetails | null;

	constructor(status: number, message: string, problem: ProblemDetails | null) {
		super(message);
		this.name = 'ApiError';
		this.status = status;
		this.problem = problem;
	}
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
	const headers = new Headers(init.headers);
	if (init.body !== undefined) {
		headers.set('Content-Type', 'application/json');
	}

	const response = await fetch(path, { ...init, headers });
	if (!response.ok) {
		let problem: ProblemDetails | null = null;
		try {
			problem = (await response.json()) as ProblemDetails;
		} catch {
			problem = null;
		}
		throw new ApiError(response.status, problem?.detail ?? response.statusText, problem);
	}
	if (response.status === 204) {
		return undefined as T;
	}
	return (await response.json()) as T;
}

export interface TaskQuery {
	status?: Status[];
	priority?: Priority[];
	search?: string;
	overdue?: boolean;
	sort?: string;
	limit?: number;
	offset?: number;
}

export function listTasks(query: TaskQuery = {}): Promise<TaskList> {
	const params = new URLSearchParams();
	for (const status of query.status ?? []) params.append('status', status);
	for (const priority of query.priority ?? []) params.append('priority', priority);
	if (query.search) params.set('search', query.search);
	if (query.overdue !== undefined) params.set('overdue', String(query.overdue));
	if (query.sort) params.set('sort', query.sort);
	if (query.limit !== undefined) params.set('limit', String(query.limit));
	if (query.offset !== undefined) params.set('offset', String(query.offset));
	const suffix = params.toString();
	return request<TaskList>(`/api/v1/tasks${suffix ? `?${suffix}` : ''}`);
}

export function getTask(id: number): Promise<Task> {
	return request<Task>(`/api/v1/tasks/${id}`);
}

export interface CreateTaskInput {
	title: string;
	note?: string;
	priority?: Priority;
	planned_date?: string;
	due_date?: string;
	due_at?: string;
	tags?: string[];
}

export function createTask(input: CreateTaskInput): Promise<Task> {
	return request<Task>('/api/v1/tasks', { method: 'POST', body: JSON.stringify(input) });
}

export type UpdateTaskInput = Partial<{
	title: string;
	note: string | null;
	priority: Priority;
	planned_date: string | null;
	due_date: string | null;
	due_at: string | null;
	timezone: string;
	tags: string[];
}>;

export function updateTask(id: number, version: number, input: UpdateTaskInput): Promise<Task> {
	return request<Task>(`/api/v1/tasks/${id}`, {
		method: 'PATCH',
		headers: { 'If-Match': `"${version}"` },
		body: JSON.stringify(input)
	});
}

function transition(id: number, version: number, action: 'complete' | 'cancel' | 'restore'): Promise<Task> {
	return request<Task>(`/api/v1/tasks/${id}/${action}`, {
		method: 'POST',
		headers: { 'If-Match': `"${version}"` }
	});
}

export const completeTask = (id: number, version: number) => transition(id, version, 'complete');
export const cancelTask = (id: number, version: number) => transition(id, version, 'cancel');
export const restoreTask = (id: number, version: number) => transition(id, version, 'restore');

export function rescheduleTask(
	id: number,
	version: number,
	input: { planned_date?: string | null; due_date?: string | null; due_at?: string | null }
): Promise<Task> {
	return request<Task>(`/api/v1/tasks/${id}/reschedule`, {
		method: 'POST',
		headers: { 'If-Match': `"${version}"` },
		body: JSON.stringify(input)
	});
}

export function listInbox(unprocessedOnly = true): Promise<InboxList> {
	return request<InboxList>(`/api/v1/inbox?unprocessed_only=${unprocessedOnly}`);
}

export function captureInbox(text: string): Promise<InboxItem> {
	return request<InboxItem>('/api/v1/inbox', { method: 'POST', body: JSON.stringify({ text }) });
}

export function convertInbox(id: number, priority?: Priority): Promise<Task> {
	return request<Task>(`/api/v1/inbox/${id}/convert`, {
		method: 'POST',
		body: JSON.stringify(priority ? { priority } : {})
	});
}

export function dismissInbox(id: number): Promise<void> {
	return request<void>(`/api/v1/inbox/${id}`, { method: 'DELETE' });
}

export function getSummary(): Promise<Summary> {
	return request<Summary>('/api/v1/summary/today');
}

export interface Attachment {
	kind: 'task' | 'task_list' | 'event' | 'event_list' | 'document';
	data: unknown;
}

export interface AssistantMessage {
	id: number;
	conversation_id: number;
	role: 'user' | 'assistant' | 'tool';
	content: string;
	tool_calls?: unknown;
	metadata?: unknown;
	attachments?: Attachment[];
	reasoning?: string;
	created_at: string;
}

export interface PendingAction {
	id: number;
	conversation_id: number;
	message_id: number;
	tool_name: string;
	arguments: Record<string, unknown>;
	preview: { summary?: string };
	status: 'pending' | 'confirmed' | 'rejected' | 'expired';
	created_at: string;
	resolved_at: string | null;
	resolution: string | null;
}

export interface Conversation {
	id: number;
	title: string | null;
	created_at: string;
	updated_at: string;
}

export interface ConversationView {
	id: number;
	title: string | null;
	created_at: string;
	updated_at: string;
	messages: AssistantMessage[];
	pending_actions: PendingAction[];
}

export interface SendMessageResult {
	conversation_id: number;
	message: AssistantMessage;
	pending_actions: PendingAction[];
}

export interface InteractionContext {
	screen?: string;
	object_type?: string;
	object_id?: string;
	title?: string;
}

export function sendAssistantMessage(
	content: string,
	conversationId?: number,
	interaction?: InteractionContext
): Promise<SendMessageResult> {
	return request<SendMessageResult>('/api/v1/assistant/messages', {
		method: 'POST',
		body: JSON.stringify({ content, conversation_id: conversationId ?? 0, interaction })
	});
}

export function listConversations(): Promise<{ data: Conversation[]; meta: { total: number } }> {
	return request<{ data: Conversation[]; meta: { total: number } }>('/api/v1/assistant/conversations');
}

export function getConversation(id: number): Promise<ConversationView> {
	return request<ConversationView>(`/api/v1/assistant/conversations/${id}`);
}

export function confirmPendingAction(id: number): Promise<{ pending_action: PendingAction; result: unknown }> {
	return request<{ pending_action: PendingAction; result: unknown }>(
		`/api/v1/assistant/pending-actions/${id}/confirm`,
		{ method: 'POST' }
	);
}

export function rejectPendingAction(id: number): Promise<{ pending_action: PendingAction }> {
	return request<{ pending_action: PendingAction }>(`/api/v1/assistant/pending-actions/${id}/reject`, {
		method: 'POST'
	});
}

export interface Insight {
	key: string;
	kind: string;
	severity: 'critical' | 'warning' | 'info';
	title: string;
	detail: string;
	count: number;
	task_ids: number[];
}

export function getInsights(): Promise<{ data: Insight[] }> {
	return request<{ data: Insight[] }>('/api/v1/insights');
}

export function dismissInsight(key: string): Promise<void> {
	return request<void>(`/api/v1/insights/${encodeURIComponent(key)}/dismiss`, { method: 'POST' });
}

export function snoozeInsight(key: string, until?: string): Promise<void> {
	return request<void>(`/api/v1/insights/${encodeURIComponent(key)}/snooze`, {
		method: 'POST',
		body: JSON.stringify(until ? { until } : {})
	});
}


export type CalendarName = 'work' | 'meetings' | 'personal';

export interface CalendarEvent {
	id: number;
	title: string;
	description: string | null;
	location: string | null;
	calendar: CalendarName;
	starts_at: string;
	ends_at: string;
	all_day: boolean;
	timezone: string;
	created_at: string;
	updated_at: string;
	version: number;
}

export interface CalendarConflict {
	kind: 'event_event' | 'event_task';
	event_ids: number[];
	task_ids?: number[];
	starts_at: string;
	ends_at: string;
}

export interface CalendarRange {
	from: string;
	to: string;
	events: CalendarEvent[];
	tasks: Task[];
	conflicts: CalendarConflict[];
}

export interface Profile {
	name: string;
	role: string;
	initials: string;
}

export interface SystemStatus {
	ok: boolean;
	version: string;
	time: string;
	database: { ok: boolean };
	assistant: { configured: boolean };
	events: { revision: number };
	pending_actions: number;
	synced_at: string;
}

export function getProfile(): Promise<Profile> {
	return request<Profile>('/api/v1/profile');
}

export function updateProfile(input: { name: string; role: string }): Promise<Profile> {
	return request<Profile>('/api/v1/profile', { method: 'PUT', body: JSON.stringify(input) });
}

export function getStatus(): Promise<SystemStatus> {
	return request<SystemStatus>('/api/v1/status');
}

export function getCalendar(
	from: string,
	to: string,
	calendars: CalendarName[] = []
): Promise<CalendarRange> {
	const params = new URLSearchParams({ from, to });
	for (const calendar of calendars) params.append('calendar', calendar);
	return request<CalendarRange>(`/api/v1/calendar?${params.toString()}`);
}

export function createEvent(input: {
	title: string;
	starts_at: string;
	ends_at?: string;
	calendar?: CalendarName;
	location?: string;
	description?: string;
	all_day?: boolean;
}): Promise<CalendarEvent> {
	return request<CalendarEvent>('/api/v1/calendar/events', {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

export function updateEvent(
	id: number,
	version: number,
	input: Partial<{
		title: string;
		starts_at: string;
		ends_at: string;
		calendar: CalendarName;
		location: string;
		description: string;
		all_day: boolean;
	}>
): Promise<CalendarEvent> {
	return request<CalendarEvent>(`/api/v1/calendar/events/${id}`, {
		method: 'PATCH',
		headers: { 'If-Match': `"${version}"` },
		body: JSON.stringify(input)
	});
}

export function deleteEvent(id: number, version: number): Promise<void> {
	return request<void>(`/api/v1/calendar/events/${id}`, {
		method: 'DELETE',
		headers: { 'If-Match': `"${version}"` }
	});
}

export type DocumentStatus = 'pending' | 'processing' | 'processed' | 'failed';

export interface DocumentTask {
	id: number;
	document_id: number;
	title: string;
	note: string | null;
	due_date: string | null;
	priority: Priority;
	confidence: number;
	source: string | null;
	status: 'proposed' | 'accepted' | 'dismissed';
	task_id: number | null;
}

export interface UploadedDocument {
	id: number;
	filename: string;
	mime: string;
	size: number;
	status: DocumentStatus;
	error: string | null;
	created_at: string;
	updated_at: string;
	tasks?: DocumentTask[];
}

export async function uploadDocument(file: File): Promise<UploadedDocument> {
	const form = new FormData();
	form.append('file', file);
	const response = await fetch('/api/v1/documents', { method: 'POST', body: form });
	if (!response.ok) {
		let detail = response.statusText;
		try {
			const problem = (await response.json()) as ProblemDetails;
			detail = problem.detail ?? detail;
		} catch {
			// keep status text
		}
		throw new ApiError(response.status, detail, null);
	}
	return (await response.json()) as UploadedDocument;
}

export function getDocument(id: number): Promise<UploadedDocument> {
	return request<UploadedDocument>(`/api/v1/documents/${id}`);
}

export function deleteDocument(id: number): Promise<void> {
	return request<void>(`/api/v1/documents/${id}`, { method: 'DELETE' });
}

export function acceptDocumentTasks(
	id: number,
	taskIds: number[]
): Promise<{ tasks: Task[]; proposals: DocumentTask[] }> {
	return request<{ tasks: Task[]; proposals: DocumentTask[] }>(`/api/v1/documents/${id}/tasks`, {
		method: 'POST',
		body: JSON.stringify({ task_ids: taskIds })
	});
}

export function reprocessDocument(id: number): Promise<{ id: number; status: DocumentStatus }> {
	return request<{ id: number; status: DocumentStatus }>(`/api/v1/documents/${id}/reprocess`, {
		method: 'POST'
	});
}

export interface DocumentList {
	data: UploadedDocument[];
	meta: { limit: number; offset: number; total: number };
}

export function listDocuments(limit = 20, offset = 0): Promise<DocumentList> {
	return request<DocumentList>(`/api/v1/documents?limit=${limit}&offset=${offset}`);
}
