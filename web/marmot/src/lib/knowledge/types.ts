export type WikiSource = {
	id: string;
	title: string;
	url: string;
	hash: string;
	field: string;
	preview?: string;
};

export type WikiPage = {
	entity_type: string;
	entity_id: string;
	title: string;
	entity_url: string;
	description: string;
	doc_entity_id: string;
	content: string;
	draft_content: string;
	draft_hash: string;
	current_source_hash: string;
	status: string;
	freshness: string;
	draft_freshness: string;
	mode: string;
	draft_mode: string;
	sources: WikiSource[];
	documents: { id: string; title: string; content: string }[];
	published_sources: WikiSource[];
	draft_sources: WikiSource[];
	error?: string;
};

export type WikiSettings = { can_write: boolean; mode: string; interval_seconds: number };
