export interface MetamodelPresentation {
	labelKey?: string;
	helpTextKey?: string;
	descriptionKey?: string;
	section?: string;
	order?: number;
}

export interface MetamodelConstraints {
	minimum?: number;
	maximum?: number;
	minLength?: number;
	maxLength?: number;
	minItems?: number;
	maxItems?: number;
}

export interface MetamodelField {
	id: string;
	type: string;
	itemType?: string;
	core: boolean;
	required: boolean;
	nullable?: boolean;
	storage: string;
	appliesTo?: string;
	values?: string[];
	validation?: MetamodelConstraints;
	presentation?: MetamodelPresentation;
}

export interface MetamodelSchema {
	formatVersion: number;
	id: string;
	version: number;
	defaultLocale: string;
	fields: MetamodelField[];
	hash: string;
	enabled: boolean;
}

export interface MetamodelViolation {
	field: string;
	code: string;
}

export interface MetamodelValidationError {
	fields: MetamodelViolation[];
}
