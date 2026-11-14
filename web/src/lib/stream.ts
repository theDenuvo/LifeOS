import {
	ApiError,
	type AssistantMessage,
	type InteractionContext,
	type PendingAction,
	type ProblemDetails
} from './api';

export interface AssistantModel {
	id: string;
	name: string;
	context_length: number;
}

export interface AssistantConfig {
	models: AssistantModel[];
	model: string;
	auto_confirm: boolean;
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
	const response = await fetch(path, init);
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
	return (await response.json()) as T;
}

export function getAssistantConfig(): Promise<AssistantConfig> {
	return requestJSON<AssistantConfig>('/api/v1/assistant/config');
}

export function updateAssistantConfig(input: {
	model?: string;
	auto_confirm?: boolean;
}): Promise<AssistantConfig> {
	return requestJSON<AssistantConfig>('/api/v1/assistant/config', {
		method: 'PUT',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(input)
	});
}

export interface StreamDone {
	conversation_id: number;
	message: AssistantMessage;
	pending_actions: PendingAction[];
}

export interface StreamTool {
	name: string;
	status: 'running' | 'done';
	auto_confirmed?: boolean;
}

/** One provider retry or model fallback inside the assistant runtime. */
export interface StreamRetry {
	model?: string;
	attempt?: number;
	fallback?: boolean;
	reason?: string;
}

export interface StreamHandlers {
	onStart?: (data: { conversation_id: number; user_message_id: number }) => void;
	onReasoning?: (delta: string) => void;
	onContent?: (delta: string) => void;
	onTool?: (event: StreamTool) => void;
	onRetry?: (event: StreamRetry) => void;
	onPendingAction?: (action: PendingAction) => void;
	onDone?: (result: StreamDone) => void;
	onError?: (message: string) => void;
}

export async function streamAssistantMessage(
	input: {
		content: string;
		conversation_id: number;
		model?: string;
		auto_confirm?: boolean;
		document_ids?: number[];
		interaction?: InteractionContext;
	},
	handlers: StreamHandlers,
	signal?: AbortSignal
): Promise<void> {
	const response = await fetch('/api/v1/assistant/messages/stream', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
		body: JSON.stringify(input),
		signal
	});
	if (!response.ok || !response.body) {
		let detail = response.statusText;
		try {
			const problem = (await response.json()) as ProblemDetails;
			detail = problem.detail ?? detail;
		} catch {
			// keep status text
		}
		throw new ApiError(response.status, detail, null);
	}

	const reader = response.body.getReader();
	const decoder = new TextDecoder();
	let buffer = '';
	let finished = false;

	const dispatch = (block: string) => {
		let event = 'message';
		const dataLines: string[] = [];
		for (const line of block.split('\n')) {
			if (line.startsWith('event:')) event = line.slice(6).trim();
			else if (line.startsWith('data:')) dataLines.push(line.slice(5).trimStart());
		}
		if (dataLines.length === 0) return;
		let data: unknown;
		try {
			data = JSON.parse(dataLines.join('\n'));
		} catch {
			return;
		}
		switch (event) {
			case 'start':
				handlers.onStart?.(data as { conversation_id: number; user_message_id: number });
				break;
			case 'reasoning':
				handlers.onReasoning?.((data as { delta?: string }).delta ?? '');
				break;
			case 'content':
				handlers.onContent?.((data as { delta?: string }).delta ?? '');
				break;
			case 'tool':
				handlers.onTool?.(data as StreamTool);
				break;
			case 'retry':
				handlers.onRetry?.(data as StreamRetry);
				break;
			case 'pending_action':
				handlers.onPendingAction?.(data as PendingAction);
				break;
			case 'done':
				finished = true;
				handlers.onDone?.(data as StreamDone);
				break;
			case 'error':
				handlers.onError?.((data as { message?: string }).message ?? 'Ошибка ассистента');
				break;
			default:
				break;
		}
	};

	while (!finished) {
		const { value, done } = await reader.read();
		if (done) break;
		buffer += decoder.decode(value, { stream: true });
		let separator = buffer.indexOf('\n\n');
		while (separator >= 0) {
			const block = buffer.slice(0, separator);
			buffer = buffer.slice(separator + 2);
			dispatch(block);
			if (finished) break;
			separator = buffer.indexOf('\n\n');
		}
	}
	if (!finished && buffer.trim()) dispatch(buffer);
}
