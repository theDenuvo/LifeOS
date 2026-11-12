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
