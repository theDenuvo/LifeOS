import type { PendingAction, UploadedDocument } from './api';
import { errorMessage, isConnectionError } from './format';
import { streamAssistantMessage, type StreamDone, type StreamHandlers } from './stream';

export interface RunTool {
	name: string;
	status: 'running' | 'done';
}

/** Everything the chat needs to render a turn that is still in flight. */
export interface RunState {
	active: boolean;
	conversationID: number;
	userContent: string;
	userAt: string;
	documents: UploadedDocument[];
	content: string;
	reasoning: string;
	tools: RunTool[];
	notice: string;
	error: string;
	pending: PendingAction[];
}

export interface RunRequest {
	content: string;
	conversation_id: number;
	model?: string;
	auto_confirm: boolean;
	document_ids: number[];
	documents: UploadedDocument[];
	interaction: { screen: string };
}

export interface RunListener {
	update: (state: RunState) => void;
	finish: (state: RunState) => void;
}

function emptyState(): RunState {
	return {
		active: false,
		conversationID: 0,
		userContent: '',
		userAt: '',
		documents: [],
		content: '',
		reasoning: '',
		tools: [],
		notice: '',
		error: '',
		pending: []
	};
}

function copy(state: RunState): RunState {
	return {
		...state,
		tools: [...state.tools],
		documents: [...state.documents],
		pending: [...state.pending]
	};
}

/**
 * Owns the assistant SSE run.
 *
 * The run deliberately lives outside the component: switching to another
 * section of the interface unmounts the chat, and a turn is the user's work,
 * not the view's. Anything the component cares about is mirrored in `RunState`,
 * so a remounted chat picks the in-flight turn up instead of aborting it.
 */
class AssistantRun {
	private state: RunState = emptyState();
	private controller: AbortController | null = null;
	private listeners = new Set<RunListener>();
	private lastRequest: RunRequest | null = null;

	get running(): boolean {
		return this.state.active;
	}

	get conversationID(): number {
		return this.state.conversationID;
	}

	get lastUserContent(): string {
		return this.lastRequest?.content ?? '';
	}

	get lastScreen(): string {
		return this.lastRequest?.interaction.screen ?? '/';
	}

	get canRetry(): boolean {
		return Boolean(this.lastRequest);
	}

	snapshot(): RunState {
		return copy(this.state);
	}

	attach(listener: RunListener): () => void {
		this.listeners.add(listener);
		listener.update(this.snapshot());
		return () => {
			this.listeners.delete(listener);
		};
	}

	private publish(): void {
		const state = this.snapshot();
		for (const listener of this.listeners) listener.update(state);
	}

	private settle(): void {
		const state = this.snapshot();
		this.state = emptyState();
		this.state.conversationID = state.conversationID;
		this.state.pending = state.pending;
		// A turn that failed while the chat was closed still owes the user an
		// explanation and a retry, so the message survives until the next run.
		this.state.error = state.error;
		this.controller = null;
		for (const listener of this.listeners) listener.finish(state);
	}

	/** Starts a turn. Does nothing while another turn is still running. */
	start(request: RunRequest): void {
		if (this.state.active) return;
		this.lastRequest = { ...request };
		this.state = {
			...emptyState(),
			active: true,
			conversationID: request.conversation_id,
			userContent: request.content,
			userAt: new Date().toISOString(),
			documents: [...request.documents]
		};
		this.controller = new AbortController();
		this.publish();
		void this.run(request, this.controller);
	}

	/** Repeats the last turn, e.g. after a provider outage or connection drop. */
	retry(): void {
		if (this.state.active || !this.lastRequest) return;
		this.start({ ...this.lastRequest });
	}

	/** Aborts the running turn (explicit "stop" or a new conversation). */
	stop(): void {
		this.controller?.abort();
	}

	/** Drops the finished turn's leftovers without touching its identifier. */
	clear(): void {
		const conversationID = this.state.conversationID;
		this.state = emptyState();
		this.state.conversationID = conversationID;
	}

	reset(): void {
		this.stop();
		this.lastRequest = null;
		this.state = emptyState();
	}

	private async run(request: RunRequest, controller: AbortController): Promise<void> {
		const scroll = () => this.publish();
		const handlers: StreamHandlers = {
			onStart: (data) => {
				if (data.conversation_id) this.state.conversationID = data.conversation_id;
				this.publish();
			},
			onReasoning: (delta) => {
				this.state.reasoning += delta;
				scroll();
			},
			onContent: (delta) => {
				this.state.content += delta;
				scroll();
			},
			onTool: (event) => {
				if (event.status === 'running') {
					this.state.tools = [...this.state.tools, { name: event.name, status: 'running' }];
				} else {
					const index = this.state.tools.map((tool) => tool.name).lastIndexOf(event.name);
					if (index >= 0) {
						this.state.tools = this.state.tools.map((tool, toolIndex) =>
							toolIndex === index ? { ...tool, status: 'done' } : tool
						);
					}
				}
				scroll();
			},
			onRetry: (event) => {
				this.state.notice = event.fallback
					? 'Провайдер ограничил запросы — переключаюсь на другую модель…'
					: `Модель не ответила — повторяю запрос (попытка ${(event.attempt ?? 1) + 1})…`;
				scroll();
			},
			onPendingAction: (action) => {
				if (action.status === 'pending') {
					this.state.pending = [
						...this.state.pending.filter((item) => item.id !== action.id),
						action
					];
				} else {
					this.state.pending = this.state.pending.filter((item) => item.id !== action.id);
				}
				scroll();
			},
			onDone: (result: StreamDone) => {
				this.state.conversationID = result.conversation_id;
				this.state.pending = result.pending_actions.filter(
					(action) => action.status === 'pending'
				);
			},
			onError: (message) => {
				this.state.error = message;
			}
		};

		try {
			await streamAssistantMessage(
				{
					content: request.content,
					conversation_id: request.conversation_id,
					model: request.model,
					auto_confirm: request.auto_confirm,
					document_ids: request.document_ids,
					interaction: request.interaction
				},
				handlers,
				controller.signal
			);
		} catch (err) {
			if ((err as DOMException)?.name !== 'AbortError') {
				this.state.error = isConnectionError(err)
					? 'Соединение с ассистентом прервалось — ответ пришёл не полностью. Нажмите «Повторить», чтобы отправить запрос заново.'
					: errorMessage(err);
			}
		} finally {
			this.state.active = false;
			this.settle();
		}
	}
}

export const assistantRun = new AssistantRun();
