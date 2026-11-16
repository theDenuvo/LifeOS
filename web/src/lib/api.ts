export type Priority = 'low' | 'medium' | 'high';
export type Status = 'open' | 'completed' | 'cancelled';

export type RepeatFreq = 'daily' | 'weekly' | 'monthly';

export interface Repeat {
	freq: RepeatFreq;
	interval: number;
	weekdays?: number[];
	until?: string;
	window_days?: number;
}

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
	estimate_minutes: number | null;
	expected_result: string | null;
	repeat: Repeat | null;
	repeat_description?: string;
	repeat_parent_id?: number | null;
}

export interface TaskList {
	data: Task[];
	meta: { limit: number; offset: number; total: number };
}

export type InboxKind = 'task' | 'idea' | 'note' | 'goal';

export interface InboxItem {
	id: number;
	text: string;
	meta: Record<string, unknown> | null;
	kind: InboxKind;
	revisit_at: string | null;
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
	revisit: RevisitItem[];
	plan?: PlanInfo | null;
}

export interface RevisitItem {
	id: number;
	text: string;
	kind: InboxKind;
	revisit_at: string;
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

export interface PlanItem {
	block_id?: number;
	task_id: number;
	title: string;
	priority: Priority;
	start: string;
	end: string;
	start_local: string;
	end_local: string;
	source?: 'auto' | 'manual' | 'assistant';
	status?: 'planned' | 'active' | 'done' | 'skipped';
	estimate_minutes?: number;
	estimate_source?: 'explicit' | 'history' | 'default';
	recurring?: boolean;
	due_at?: string;
	due_date?: string;
}

export interface PlanBusy {
	label: string;
	kind: 'event' | 'block';
	start: string;
	end: string;
	task_id?: number;
}

export interface PlanUnplaced {
	task_id: number;
	title: string;
	estimate_minutes: number;
	reason: string;
}

export interface DayPlan {
	date: string;
	working: boolean;
	/** A day that is already over is reported as such, not planned again. */
	past: boolean;
	workday_start: string;
	workday_end: string;
	free_minutes: number;
	planned_minutes: number;
	blocks: PlanItem[];
	proposed: PlanItem[];
	busy: PlanBusy[];
	unplaced: PlanUnplaced[];
}

export interface PlanInfo {
	working: boolean;
	workday_start: string;
	workday_end: string;
	free_minutes: number;
	planned_minutes: number;
	blocks: number;
	proposed: number;
}

export interface PlanMove {
	task_id: number;
	title: string;
	from?: string;
	to: string;
	reason: string;
	estimate_minutes: number;
}

export interface PlanProposal {
	generated_at: string;
	horizon_days: number;
	summary: string;
	moves: PlanMove[];
	placements: { task_id: number; date: string; start: string; end: string }[];
	unplaced: PlanUnplaced[];
	/** Work that already has its time and is therefore not proposed again. */
	already_placed: PlanKept[];
	days: { date: string; working: boolean; free_minutes: number; planned_minutes: number }[];
}

export interface PlanKept {
	task_id: number;
	title: string;
	reason: string;
	start: string;
	end: string;
	source: 'auto' | 'manual' | 'assistant';
}

/** A reminder that is due and has not been acknowledged by this browser yet. */
export interface DueReminder {
	reminder_id: number;
	task_id: number;
	title: string;
	kind: 'lead' | 'due' | 'overdue';
	priority: Priority;
	fire_at: string;
	due_at?: string;
	due_date?: string;
	estimate_minutes?: number;
	note?: string;
	repeat?: string;
	overdue: boolean;
	notified: boolean;
}

export interface Reminder {
	id: number;
	kind: 'lead' | 'due' | 'overdue';
	status: 'pending' | 'sent' | 'dismissed';
	fire_at: string;
	sent_at?: string;
}

export function getDayPlan(date: string): Promise<DayPlan> {
	return request<DayPlan>(`/api/v1/plan/day?date=${date}`);
}

export function applyPlan(
	replaceDates: string[],
	items: { task_id: number; start: string; end: string }[]
): Promise<{ applied: PlanItem[]; skipped: string[] }> {
	return request(`/api/v1/plan/apply`, {
		method: 'POST',
		body: JSON.stringify({ replace_dates: replaceDates, items })
	});
}

/**
 * getDueReminders returns every reminder that fired and was not acknowledged yet.
 * This is the durable path: it works after a restart and after a closed tab,
 * unlike a live event that only reaches connected clients.
 */
export function getDueReminders(): Promise<{ data: DueReminder[]; server_time: string }> {
	return request(`/api/v1/reminders`);
}

/** ackReminder records that the reminder was actually shown to the user. */
export function ackReminder(id: number): Promise<void> {
	return request(`/api/v1/reminders/${id}/ack`, { method: 'POST' });
}

export function getPlanProposal(days = 7): Promise<PlanProposal> {
	return request<PlanProposal>(`/api/v1/plan/replan?days=${days}`);
}

export function deletePlanBlock(id: number): Promise<void> {
	return request<void>(`/api/v1/plan/blocks/${id}`, { method: 'DELETE' });
}

export function getTaskReminders(id: number): Promise<{ data: Reminder[] }> {
	return request<{ data: Reminder[] }>(`/api/v1/tasks/${id}/reminders`);
}

export function estimateTask(title: string, tags: string[] = []): Promise<{
	minutes: number;
	source: 'history' | 'default' | 'unknown';
	samples: number;
	based_on?: string[];
	question?: string;
}> {
	const params = new URLSearchParams({ title });
	if (tags.length > 0) params.set('tags', tags.join(','));
	return request(`/api/v1/tasks/estimate?${params.toString()}`);
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
	estimate_minutes?: number;
	expected_result?: string;
	repeat?: Repeat;
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
	estimate_minutes: number | null;
	expected_result: string | null;
	repeat: Repeat | null;
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

export function listInbox(
	unprocessedOnly = true,
	options: { kind?: InboxKind; revisit?: 'due' } = {}
): Promise<InboxList> {
	const params = new URLSearchParams({ unprocessed_only: String(unprocessedOnly) });
	if (options.kind) params.set('kind', options.kind);
	if (options.revisit) params.set('revisit', options.revisit);
	return request<InboxList>(`/api/v1/inbox?${params.toString()}`);
}

export function captureInbox(
	text: string,
	options: { kind?: InboxKind; revisit_at?: string } = {}
): Promise<InboxItem> {
	return request<InboxItem>('/api/v1/inbox', {
		method: 'POST',
		body: JSON.stringify({ text, ...options })
	});
}

export function updateInbox(
	id: number,
	input: { kind?: InboxKind; revisit_at?: string | null }
): Promise<InboxItem> {
	return request<InboxItem>(`/api/v1/inbox/${id}`, {
		method: 'PATCH',
		body: JSON.stringify(input)
	});
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
	kind: 'task' | 'task_list' | 'event' | 'event_list' | 'document' | 'workspace';
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
	kind?: 'main' | 'project';
	purpose?: string | null;
	source_conversation_id?: number | null;
	created_at: string;
	updated_at: string;
}

export interface ConversationView {
	id: number;
	title: string | null;
	kind?: 'main' | 'project';
	purpose?: string | null;
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

export function listConversations(
	kind?: 'main' | 'project'
): Promise<{ data: Conversation[]; meta: { total: number } }> {
	const query = kind ? `?kind=${kind}` : '';
	return request<{ data: Conversation[]; meta: { total: number } }>(
		`/api/v1/assistant/conversations${query}`
	);
}

export function createWorkspace(title: string, purpose?: string): Promise<Conversation> {
	return request<Conversation>('/api/v1/assistant/workspaces', {
		method: 'POST',
		body: JSON.stringify({ title, purpose: purpose ?? '' })
	});
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
	/** Roadmap stage 5: the concrete next action the user can accept. */
	proposal?: string;
}

export type OutreachOutcome = 'pending' | 'acknowledged' | 'dismissed' | 'snoozed';

export interface Outreach {
	id: number;
	conversation_id: number;
	message_id: number;
	insight_key: string;
	kind: string;
	severity: 'critical' | 'warning' | 'info';
	title: string;
	reason: string;
	proposal: string;
	outcome: OutreachOutcome;
	created_at: string;
	decided_at?: string | null;
	snoozed_until?: string | null;
}

export function getInsights(): Promise<{ data: Insight[] }> {
	return request<{ data: Insight[] }>('/api/v1/insights');
}

export function listOutreach(limit = 30): Promise<{ data: Outreach[]; total: number }> {
	return request(`/api/v1/outreach?limit=${limit}`);
}

export function acknowledgeOutreach(id: number): Promise<void> {
	return request(`/api/v1/outreach/${id}/ack`, { method: 'POST' });
}

export function dismissOutreach(id: number): Promise<void> {
	return request(`/api/v1/outreach/${id}/dismiss`, { method: 'POST' });
}

export function snoozeOutreach(id: number, until?: string): Promise<void> {
	return request(`/api/v1/outreach/${id}/snooze`, {
		method: 'POST',
		body: JSON.stringify({ until })
	});
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

export type MemoryArea = 'personal' | 'knowledge';
export type MemoryCertainty = 'fact' | 'assumption' | 'inference';
export type MemoryStatus = 'active' | 'disputed' | 'superseded';
export type MemoryCategory =
	| 'preference'
	| 'habit'
	| 'circumstance'
	| 'fact'
	| 'agreement'
	| 'context'
	| 'rule'
	| 'instruction'
	| 'summary'
	| 'reference'
	| 'research'
	| 'idea'
	| 'documentation'
	| 'note';

export interface MemorySource {
	kind: 'user' | 'assistant' | 'conversation' | 'document' | 'task' | 'event' | 'system';
	source_id?: number;
	label: string;
	excerpt?: string;
	observed_at: string;
}

export interface MemoryLink {
	id: number;
	entry_id: number;
	target_type: 'memory' | 'task' | 'event' | 'conversation' | 'document' | 'inbox' | 'goal';
	target_id: number;
	relation: string;
	created_at: string;
}

export interface MemoryEntry {
	id: number;
	area: MemoryArea;
	category: MemoryCategory;
	title: string;
	content: string;
	certainty: MemoryCertainty;
	confidence: number;
	status: MemoryStatus;
	superseded_by: number | null;
	entities: string[];
	sources: MemorySource[];
	created_at: string;
	updated_at: string;
	version: number;
	links?: MemoryLink[];
}

export interface MemoryConflict {
	id: number;
	reason: string;
	status: 'pending' | 'resolved';
	resolution?: string;
	created_at: string;
	resolved_at: string | null;
	existing: MemoryEntry;
	candidate: MemoryEntry;
}

export interface MemoryStats {
	personal: number;
	knowledge: number;
	pending_conflicts: number;
}

export interface MemoryQuery {
	query?: string;
	area?: MemoryArea;
	category?: MemoryCategory;
	limit?: number;
	offset?: number;
}

export function listMemory(query: MemoryQuery = {}): Promise<{
	data: MemoryEntry[];
	meta: { limit: number; offset: number; total: number };
}> {
	const params = new URLSearchParams();
	if (query.query) params.set('q', query.query);
	if (query.area) params.set('area', query.area);
	if (query.category) params.set('category', query.category);
	if (query.limit !== undefined) params.set('limit', String(query.limit));
	if (query.offset !== undefined) params.set('offset', String(query.offset));
	const suffix = params.toString();
	return request(`/api/v1/memory${suffix ? `?${suffix}` : ''}`);
}

export function getMemory(id: number): Promise<MemoryEntry> {
	return request<MemoryEntry>(`/api/v1/memory/${id}`);
}

export function createMemory(input: {
	title: string;
	content: string;
	category: MemoryCategory;
	certainty?: MemoryCertainty;
	entities?: string[];
}): Promise<{ entry: MemoryEntry; duplicate: boolean; conflict_id?: number }> {
	return request('/api/v1/memory', { method: 'POST', body: JSON.stringify(input) });
}

export function updateMemory(
	id: number,
	version: number,
	input: Partial<{
		title: string;
		content: string;
		category: MemoryCategory;
		certainty: MemoryCertainty;
		confidence: number;
		entities: string[];
	}>
): Promise<MemoryEntry> {
	return request<MemoryEntry>(`/api/v1/memory/${id}`, {
		method: 'PATCH',
		headers: { 'If-Match': `"${version}"` },
		body: JSON.stringify(input)
	});
}

export function deleteMemory(id: number, version: number): Promise<void> {
	return request<void>(`/api/v1/memory/${id}`, {
		method: 'DELETE',
		headers: { 'If-Match': `"${version}"` }
	});
}

export function linkMemory(
	id: number,
	input: { target_type: MemoryLink['target_type']; target_id: number; relation?: string }
): Promise<MemoryLink> {
	return request<MemoryLink>(`/api/v1/memory/${id}/links`, {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

export function unlinkMemory(id: number, linkId: number): Promise<void> {
	return request<void>(`/api/v1/memory/${id}/links/${linkId}`, { method: 'DELETE' });
}

export function getMemoryStats(): Promise<MemoryStats> {
	return request<MemoryStats>('/api/v1/memory/stats');
}

export function listMemoryConflicts(all = false): Promise<{
	data: MemoryConflict[];
	meta: { total: number };
}> {
	return request(`/api/v1/memory/conflicts${all ? '?all=true' : ''}`);
}

export function resolveMemoryConflict(
	id: number,
	input: { resolution: 'existing' | 'candidate' | 'both' | 'merge'; merged_title?: string; merged_content?: string }
): Promise<{ id: number; resolution: string }> {
	return request(`/api/v1/memory/conflicts/${id}/resolve`, {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

// ─── Цели (долгосрочные цели и инициативы) ───────────────────────────────────

export type GoalStatus = 'proposed' | 'active' | 'paused' | 'achieved' | 'abandoned';
export type GoalMilestoneStatus = 'pending' | 'in_progress' | 'done' | 'skipped';
export type GoalTargetType = 'conversation' | 'task' | 'event' | 'document' | 'memory' | 'inbox';

export interface GoalMilestone {
	id: number;
	position: number;
	title: string;
	criteria: string;
	status: GoalMilestoneStatus;
	due_date: string | null;
	completed_at: string | null;
	version: number;
}

export interface GoalLink {
	id: number;
	target_type: GoalTargetType;
	target_id: number;
	relation: string;
	created_at: string;
}

export interface GoalProgressEvent {
	id: number;
	kind: 'goal_created' | 'approved' | 'milestone' | 'task_done' | 'note' | 'status' | 'proposal';
	summary: string;
	source_type?: GoalTargetType;
	source_id?: number;
	recorded_at: string;
}

export interface GoalProposal {
	id: number;
	summary: string;
	status: 'pending' | 'applied' | 'rejected';
	changes?: Record<string, unknown>;
	created_at: string;
	decided_at?: string | null;
}

export interface GoalProgress {
	milestones_total: number;
	milestones_done: number;
	linked_tasks: number;
	linked_tasks_done: number;
	percent: number | null;
	last_activity_at: string | null;
}

export interface GoalSignals {
	stalled: boolean;
	stall_reason?: string;
	overdue: boolean;
	no_open_work: boolean;
	pending_proposal: boolean;
}

export interface Goal {
	id: number;
	title: string;
	description: string;
	status: GoalStatus;
	target_date: string | null;
	approved_at: string | null;
	created_at: string;
	updated_at: string;
	version: number;
	milestones: GoalMilestone[];
	links: GoalLink[];
	proposals: GoalProposal[];
	progress_log: GoalProgressEvent[];
	progress?: GoalProgress;
	signals?: GoalSignals;
}

export interface GoalList {
	data: Goal[];
	meta: { limit: number; offset: number; total: number };
}

export interface GoalCreateInput {
	title: string;
	description?: string;
	target_date?: string;
	milestones?: { title: string; criteria?: string; due_date?: string }[];
}

export interface GoalProposalInput {
	summary: string;
	title?: string;
	description?: string;
	target_date?: string;
	clear_target_date?: boolean;
	add_milestones?: { title: string; criteria?: string; due_date?: string }[];
	complete_milestone_ids?: number[];
	skip_milestone_ids?: number[];
	reopen_milestone_ids?: number[];
	remove_milestone_ids?: number[];
}

export function listGoals(query: { status?: GoalStatus; q?: string; limit?: number; offset?: number } = {}): Promise<GoalList> {
	const params = new URLSearchParams();
	if (query.status) params.set('status', query.status);
	if (query.q) params.set('q', query.q);
	if (query.limit !== undefined) params.set('limit', String(query.limit));
	if (query.offset !== undefined) params.set('offset', String(query.offset));
	const suffix = params.toString();
	return request<GoalList>(`/api/v1/goals${suffix ? `?${suffix}` : ''}`);
}

export function getGoal(id: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${id}`);
}

export function createGoal(input: GoalCreateInput): Promise<Goal> {
	return request<Goal>('/api/v1/goals', { method: 'POST', body: JSON.stringify(input) });
}

export function updateGoal(
	id: number,
	version: number,
	input: Partial<{ title: string; description: string; target_date: string; clear_target_date: boolean }>
): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${id}`, {
		method: 'PATCH',
		body: JSON.stringify({ ...input, expected_version: version })
	});
}

export function deleteGoal(id: number): Promise<void> {
	return request<void>(`/api/v1/goals/${id}`, { method: 'DELETE' });
}

export function approveGoal(id: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${id}/approve`, { method: 'POST' });
}

export function changeGoalStatus(id: number, status: Exclude<GoalStatus, 'proposed'>): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${id}/status`, {
		method: 'POST',
		body: JSON.stringify({ status })
	});
}

export function createGoalMilestone(
	id: number,
	input: { title: string; criteria?: string; due_date?: string }
): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${id}/milestones`, { method: 'POST', body: JSON.stringify(input) });
}

export function updateGoalMilestone(
	goalId: number,
	milestoneId: number,
	version: number,
	input: Partial<{ title: string; criteria: string; status: GoalMilestoneStatus; due_date: string; clear_due_date: boolean }>
): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/milestones/${milestoneId}`, {
		method: 'PATCH',
		body: JSON.stringify({ ...input, expected_version: version })
	});
}

export function deleteGoalMilestone(goalId: number, milestoneId: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/milestones/${milestoneId}`, { method: 'DELETE' });
}

export function createGoalProposal(goalId: number, input: GoalProposalInput): Promise<{ proposal: GoalProposal }> {
	return request<{ proposal: GoalProposal }>(`/api/v1/goals/${goalId}/proposals`, {
		method: 'POST',
		body: JSON.stringify(input)
	});
}

export function applyGoalProposal(goalId: number, proposalId: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/proposals/${proposalId}/apply`, { method: 'POST' });
}

export function rejectGoalProposal(goalId: number, proposalId: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/proposals/${proposalId}/reject`, { method: 'POST' });
}

export function linkGoal(
	goalId: number,
	input: { target_type: GoalTargetType; target_id: number; relation: string }
): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/links`, { method: 'POST', body: JSON.stringify(input) });
}

export function unlinkGoal(goalId: number, linkId: number): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/links/${linkId}`, { method: 'DELETE' });
}

export function recordGoalProgress(
	goalId: number,
	input: { summary: string; source_type?: GoalTargetType; source_id?: number }
): Promise<Goal> {
	return request<Goal>(`/api/v1/goals/${goalId}/progress`, { method: 'POST', body: JSON.stringify(input) });
}
