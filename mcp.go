package main

// MCP server surface for featmap. Exposes a subset of the workspace API as
// Model Context Protocol tools, intended for local LLM-driven automation.
//
// Auth: Authorization: Bearer <api-key> header (see mware.go User()).
// Transport: Streamable HTTP at /mcp (Stateless mode -- no session retention).
// Workspace context: every tool takes workspace_id as a tool argument rather
// than the legacy `Workspace:` HTTP header, so a single key can drive any
// workspace the owning account belongs to.
//
// Tool handlers are exposed as named package-level functions (mcpFoo) so the
// test suite can invoke them directly without going through the SDK transport
// layer. buildMCPServer() wires those handlers in via the generic withService
// helper, which centralises auth + workspace resolution.
//
// Pre-existing transaction bug warning: middleware Transaction() in mware.go
// always commits regardless of handler outcome (next.ServeHTTP wrapped in
// `return nil`). Tool handlers MUST NOT panic mid-mutation; partial state
// will be committed. Surface errors via returned error value instead.

import (
	"context"
	"errors"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	uuid "github.com/satori/go.uuid"
)

func newUUID() string { return uuid.Must(uuid.NewV4(), nil).String() }

func envFromContext(ctx context.Context) *Env {
	env, _ := ctx.Value(contextKey).(*Env)
	return env
}

// resolveWorkspace loads the member + workspace + subscription for the
// authenticated account against the given workspace ID and stashes them on
// the service. Must run before any workspace-scoped service call.
func resolveWorkspace(s Service, workspaceID string) error {
	if workspaceID == "" {
		return errors.New("workspace_id required")
	}
	acc := s.GetAccountObject()
	if acc == nil {
		return errors.New("not authenticated")
	}
	m, err := s.GetMember(acc.ID, workspaceID)
	if err != nil || m == nil {
		return errors.New("not a member of workspace")
	}
	ws, err := s.GetRepoObject().GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	s.SetMemberObject(m)
	s.SetWorkspaceObject(ws)
	sub := s.GetSubscriptionByWorkspace(workspaceID)
	if sub != nil {
		s.SetSubscriptionObject(sub)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tool argument types -- one per tool. Drives JSON schema generation via
// jsonschema struct tags.
// ---------------------------------------------------------------------------

type emptyArgs struct{}

type workspaceArgs struct {
	WorkspaceID string `json:"workspace_id" jsonschema:"the workspace UUID"`
}

type getBoardArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
}

type createFeatureArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	SubWorkflowID string `json:"subworkflow_id" jsonschema:"target subworkflow (column) UUID"`
	MilestoneID   string `json:"milestone_id" jsonschema:"target milestone (release) UUID"`
	Title         string `json:"title"`
}

type renameFeatureArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	Title       string `json:"title"`
}

type updateFeatureDescArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	Description string `json:"description"`
}

type moveFeatureArgs struct {
	WorkspaceID     string `json:"workspace_id"`
	FeatureID       string `json:"feature_id"`
	ToMilestoneID   string `json:"to_milestone_id"`
	ToSubWorkflowID string `json:"to_subworkflow_id"`
	Index           int    `json:"index" jsonschema:"position within the target cell, 0-based"`
}

type deleteFeatureArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
}

type addCommentArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	Body        string `json:"body"`
}

type createProjectArgs struct {
	WorkspaceID string `json:"workspace_id"`
	Title       string `json:"title"`
}

type createMilestoneArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
}

type createWorkflowArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Title       string `json:"title"`
}

type createSubWorkflowArgs struct {
	WorkspaceID string `json:"workspace_id"`
	WorkflowID  string `json:"workflow_id"`
	Title       string `json:"title"`
}

// Color values accepted by every set_*_color tool mirror the server-side
// validator in service.go colorIsValid(): WHITE, GREY, RED, ORANGE, YELLOW,
// GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK. Struct tags repeat the list because
// jsonschema struct-tag inference only takes a string literal.

type setFeatureColorArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	Color       string `json:"color" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
}

type setMilestoneColorArgs struct {
	WorkspaceID string `json:"workspace_id"`
	MilestoneID string `json:"milestone_id"`
	Color       string `json:"color" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
}

type setWorkflowColorArgs struct {
	WorkspaceID string `json:"workspace_id"`
	WorkflowID  string `json:"workflow_id"`
	Color       string `json:"color" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
}

type setSubWorkflowColorArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	SubWorkflowID string `json:"subworkflow_id"`
	Color         string `json:"color" jsonschema:"color name; one of: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK"`
}

type moveMilestoneArgs struct {
	WorkspaceID string `json:"workspace_id"`
	MilestoneID string `json:"milestone_id"`
	Index       int    `json:"index" jsonschema:"new 0-based position among sibling milestones"`
}

type moveWorkflowArgs struct {
	WorkspaceID string `json:"workspace_id"`
	WorkflowID  string `json:"workflow_id"`
	Index       int    `json:"index" jsonschema:"new 0-based position among sibling workflows in the project"`
}

type moveSubWorkflowArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	SubWorkflowID string `json:"subworkflow_id"`
	ToWorkflowID  string `json:"to_workflow_id" jsonschema:"workflow UUID the subworkflow should belong to after the move; may be the same workflow it is currently in"`
	Index         int    `json:"index" jsonschema:"new 0-based position within the target workflow"`
}

// Avatars are constrained to a fixed set of asset slugs. Mirrors
// validAvatar() in service.go.
type createPersonaArgs struct {
	WorkspaceID string `json:"workspace_id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar" jsonschema:"avatar slug; one of: avatar00, avatar01, avatar02, avatar03, avatar04, avatar05, avatar06, avatar07, avatar08"`
	Role        string `json:"role,omitempty" jsonschema:"short role descriptor (e.g. 'power user', 'admin'); max 200 chars"`
	Description string `json:"description,omitempty" jsonschema:"markdown description of the persona; max 10000 chars"`
	WorkflowID  string `json:"workflow_id,omitempty" jsonschema:"optional workflow UUID; if provided the persona is also attached to that workflow"`
}

type updatePersonaArgs struct {
	WorkspaceID string `json:"workspace_id"`
	PersonaID   string `json:"persona_id"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar" jsonschema:"avatar slug; one of: avatar00, avatar01, avatar02, avatar03, avatar04, avatar05, avatar06, avatar07, avatar08"`
	Role        string `json:"role,omitempty"`
	Description string `json:"description,omitempty"`
}

type deletePersonaArgs struct {
	WorkspaceID string `json:"workspace_id"`
	PersonaID   string `json:"persona_id"`
}

type attachPersonaArgs struct {
	WorkspaceID string `json:"workspace_id"`
	PersonaID   string `json:"persona_id"`
	WorkflowID  string `json:"workflow_id"`
}

type detachPersonaArgs struct {
	WorkspaceID       string `json:"workspace_id"`
	WorkflowPersonaID string `json:"workflow_persona_id" jsonschema:"the workflow-persona link UUID returned by attach_persona_to_workflow or visible in get_board.workflowPersonas[]"`
}

// Status tools use a single string arg with two accepted values, so the LLM
// can both close and re-open via the same tool name per entity.

type setFeatureStatusArgs struct {
	WorkspaceID string `json:"workspace_id"`
	FeatureID   string `json:"feature_id"`
	Status      string `json:"status" jsonschema:"OPEN or CLOSED"`
}

type setMilestoneStatusArgs struct {
	WorkspaceID string `json:"workspace_id"`
	MilestoneID string `json:"milestone_id"`
	Status      string `json:"status" jsonschema:"OPEN or CLOSED"`
}

type setWorkflowStatusArgs struct {
	WorkspaceID string `json:"workspace_id"`
	WorkflowID  string `json:"workflow_id"`
	Status      string `json:"status" jsonschema:"OPEN or CLOSED"`
}

type setSubWorkflowStatusArgs struct {
	WorkspaceID   string `json:"workspace_id"`
	SubWorkflowID string `json:"subworkflow_id"`
	Status        string `json:"status" jsonschema:"OPEN or CLOSED"`
}

// ---------------------------------------------------------------------------
// Tool response types. Promoted to named types so test code can assert on the
// concrete shape and so MCP's structured output schema generation picks up
// proper field names.
// ---------------------------------------------------------------------------

type listWorkspacesResult struct {
	Workspaces []*Workspace `json:"workspaces"`
}

type listProjectsResult struct {
	Projects []*Project `json:"projects"`
}

type boardResult struct {
	Project          *Project           `json:"project"`
	Milestones       []*Milestone       `json:"milestones"`
	Workflows        []*Workflow        `json:"workflows"`
	SubWorkflows     []*SubWorkflow     `json:"subWorkflows"`
	Features         []*Feature         `json:"features"`
	FeatureComments  []*FeatureComment  `json:"featureComments"`
	Personas         []*Persona         `json:"personas"`
	WorkflowPersonas []*WorkflowPersona `json:"workflowPersonas"`
}

type okResult struct {
	OK bool `json:"ok"`
}

// ---------------------------------------------------------------------------
// Tool handlers. Each handler is a package-level function so tests can invoke
// it directly without going through the SDK transport. Registration in
// buildMCPServer wires them in via withService().
// ---------------------------------------------------------------------------

func mcpListWorkspaces(ctx context.Context, s Service, _ emptyArgs) (listWorkspacesResult, error) {
	return listWorkspacesResult{Workspaces: s.GetWorkspaces()}, nil
}

func mcpListProjects(ctx context.Context, s Service, _ workspaceArgs) (listProjectsResult, error) {
	return listProjectsResult{Projects: s.GetProjects()}, nil
}

func mcpGetBoard(ctx context.Context, s Service, a getBoardArgs) (boardResult, error) {
	project := s.GetProject(a.ProjectID)
	if project == nil {
		return boardResult{}, errors.New("project not found")
	}
	return boardResult{
		Project:          project,
		Milestones:       s.GetMilestonesByProject(a.ProjectID),
		Workflows:        s.GetWorkflowsByProject(a.ProjectID),
		SubWorkflows:     s.GetSubWorkflowsByProject(a.ProjectID),
		Features:         s.GetFeaturesByProject(a.ProjectID),
		FeatureComments:  s.GetFeatureCommentsByProject(a.ProjectID),
		Personas:         s.GetPersonasByProject(a.ProjectID),
		WorkflowPersonas: s.GetWorkflowPersonasByProject(a.ProjectID),
	}, nil
}

func mcpCreateProject(ctx context.Context, s Service, a createProjectArgs) (*Project, error) {
	return s.CreateProjectWithID(newUUID(), a.Title)
}

func mcpCreateMilestone(ctx context.Context, s Service, a createMilestoneArgs) (*Milestone, error) {
	return s.CreateMilestoneWithID(newUUID(), a.ProjectID, a.Title)
}

func mcpCreateWorkflow(ctx context.Context, s Service, a createWorkflowArgs) (*Workflow, error) {
	return s.CreateWorkflowWithID(newUUID(), a.ProjectID, a.Title)
}

func mcpCreateSubWorkflow(ctx context.Context, s Service, a createSubWorkflowArgs) (*SubWorkflow, error) {
	return s.CreateSubWorkflowWithID(newUUID(), a.WorkflowID, a.Title)
}

func mcpCreateFeature(ctx context.Context, s Service, a createFeatureArgs) (*Feature, error) {
	return s.CreateFeatureWithID(newUUID(), a.SubWorkflowID, a.MilestoneID, a.Title)
}

func mcpRenameFeature(ctx context.Context, s Service, a renameFeatureArgs) (*Feature, error) {
	return s.RenameFeature(a.FeatureID, a.Title)
}

func mcpUpdateFeatureDescription(ctx context.Context, s Service, a updateFeatureDescArgs) (*Feature, error) {
	return s.UpdateFeatureDescription(a.FeatureID, a.Description)
}

func mcpMoveFeature(ctx context.Context, s Service, a moveFeatureArgs) (*Feature, error) {
	return s.MoveFeature(a.FeatureID, a.ToMilestoneID, a.ToSubWorkflowID, a.Index)
}

func mcpDeleteFeature(ctx context.Context, s Service, a deleteFeatureArgs) (okResult, error) {
	if err := s.DeleteFeature(a.FeatureID); err != nil {
		return okResult{OK: false}, err
	}
	return okResult{OK: true}, nil
}

func mcpAddComment(ctx context.Context, s Service, a addCommentArgs) (*FeatureComment, error) {
	return s.CreateFeatureCommentWithID(newUUID(), a.FeatureID, a.Body)
}

func mcpSetFeatureColor(ctx context.Context, s Service, a setFeatureColorArgs) (*Feature, error) {
	return s.ChangeColorOnFeature(a.FeatureID, a.Color)
}

func mcpSetMilestoneColor(ctx context.Context, s Service, a setMilestoneColorArgs) (*Milestone, error) {
	return s.ChangeColorOnMilestone(a.MilestoneID, a.Color)
}

func mcpSetWorkflowColor(ctx context.Context, s Service, a setWorkflowColorArgs) (*Workflow, error) {
	return s.ChangeColorOnWorkflow(a.WorkflowID, a.Color)
}

func mcpSetSubWorkflowColor(ctx context.Context, s Service, a setSubWorkflowColorArgs) (*SubWorkflow, error) {
	return s.ChangeColorOnSubWorkflow(a.SubWorkflowID, a.Color)
}

func mcpMoveMilestone(ctx context.Context, s Service, a moveMilestoneArgs) (*Milestone, error) {
	return s.MoveMilestone(a.MilestoneID, a.Index)
}

func mcpMoveWorkflow(ctx context.Context, s Service, a moveWorkflowArgs) (*Workflow, error) {
	return s.MoveWorkflow(a.WorkflowID, a.Index)
}

func mcpMoveSubWorkflow(ctx context.Context, s Service, a moveSubWorkflowArgs) (*SubWorkflow, error) {
	return s.MoveSubWorkflow(a.SubWorkflowID, a.ToWorkflowID, a.Index)
}

func mcpSetFeatureStatus(ctx context.Context, s Service, a setFeatureStatusArgs) (*Feature, error) {
	switch a.Status {
	case "OPEN":
		return s.OpenFeature(a.FeatureID)
	case "CLOSED":
		return s.CloseFeature(a.FeatureID)
	default:
		return nil, errors.New("status must be OPEN or CLOSED")
	}
}

func mcpSetMilestoneStatus(ctx context.Context, s Service, a setMilestoneStatusArgs) (*Milestone, error) {
	switch a.Status {
	case "OPEN":
		return s.OpenMilestone(a.MilestoneID)
	case "CLOSED":
		return s.CloseMilestone(a.MilestoneID)
	default:
		return nil, errors.New("status must be OPEN or CLOSED")
	}
}

func mcpSetWorkflowStatus(ctx context.Context, s Service, a setWorkflowStatusArgs) (*Workflow, error) {
	switch a.Status {
	case "OPEN":
		return s.OpenWorkflow(a.WorkflowID)
	case "CLOSED":
		return s.CloseWorkflow(a.WorkflowID)
	default:
		return nil, errors.New("status must be OPEN or CLOSED")
	}
}

func mcpSetSubWorkflowStatus(ctx context.Context, s Service, a setSubWorkflowStatusArgs) (*SubWorkflow, error) {
	switch a.Status {
	case "OPEN":
		return s.OpenSubWorkflow(a.SubWorkflowID)
	case "CLOSED":
		return s.CloseSubWorkflow(a.SubWorkflowID)
	default:
		return nil, errors.New("status must be OPEN or CLOSED")
	}
}

func mcpCreatePersona(ctx context.Context, s Service, a createPersonaArgs) (*Persona, error) {
	return s.CreatePersonaWithID(newUUID(), a.ProjectID, a.Avatar, a.Name, a.Role, a.Description, a.WorkflowID, newUUID())
}

func mcpUpdatePersona(ctx context.Context, s Service, a updatePersonaArgs) (*Persona, error) {
	return s.UpdatePersona(a.PersonaID, a.Avatar, a.Name, a.Role, a.Description)
}

func mcpDeletePersona(ctx context.Context, s Service, a deletePersonaArgs) (okResult, error) {
	if err := s.DeletePersona(a.PersonaID); err != nil {
		return okResult{OK: false}, err
	}
	return okResult{OK: true}, nil
}

func mcpAttachPersonaToWorkflow(ctx context.Context, s Service, a attachPersonaArgs) (*WorkflowPersona, error) {
	return s.CreateWorkflowPersonaWithID(newUUID(), a.WorkflowID, a.PersonaID)
}

func mcpDetachPersonaFromWorkflow(ctx context.Context, s Service, a detachPersonaArgs) (okResult, error) {
	if err := s.DeleteWorkflowPersona(a.WorkflowPersonaID); err != nil {
		return okResult{OK: false}, err
	}
	return okResult{OK: true}, nil
}

// ---------------------------------------------------------------------------
// withService is a tool-handler decorator that pulls the per-request Service
// out of context and short-circuits if auth middleware didn't populate it.
// ---------------------------------------------------------------------------

func withService[In, Out any](
	resolveWS func(In) string,
	fn func(context.Context, Service, In) (Out, error),
) mcpsdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		var zero Out
		env := envFromContext(ctx)
		if env == nil || env.Service == nil {
			return nil, zero, errors.New("service not initialized")
		}
		s := env.Service
		if s.GetAccountObject() == nil {
			return nil, zero, errors.New("unauthenticated")
		}
		if wsID := resolveWS(in); wsID != "" {
			if err := resolveWorkspace(s, wsID); err != nil {
				return nil, zero, err
			}
		}
		out, err := fn(ctx, s, in)
		return nil, out, err
	}
}

// buildMCPServer wires every tool. Called once at startup; the same server
// instance is reused across all sessions (safe -- tools are stateless and
// pull per-request state from context).
func buildMCPServer() *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "featmap",
		Version: "0.1.0",
	}, nil)

	add(srv, "list_workspaces",
		"List all workspaces the authenticated account belongs to. Call first to discover workspace IDs.",
		func(emptyArgs) string { return "" }, mcpListWorkspaces)

	add(srv, "list_projects",
		"List all projects in a workspace.",
		func(a workspaceArgs) string { return a.WorkspaceID }, mcpListProjects)

	add(srv, "get_board",
		"Fetch the entire story map for a project: milestones, workflows, subworkflows, features, feature comments, personas, workflow personas.",
		func(a getBoardArgs) string { return a.WorkspaceID }, mcpGetBoard)

	add(srv, "create_project",
		"Create a new project in a workspace. A project owns the entire story map (milestones, workflows, subworkflows, features).",
		func(a createProjectArgs) string { return a.WorkspaceID }, mcpCreateProject)

	add(srv, "create_milestone",
		"Create a milestone (release column, horizontal) inside a project. Milestones group features by planned release.",
		func(a createMilestoneArgs) string { return a.WorkspaceID }, mcpCreateMilestone)

	add(srv, "create_workflow",
		"Create a workflow (activity, top row) inside a project. Workflows group subworkflows.",
		func(a createWorkflowArgs) string { return a.WorkspaceID }, mcpCreateWorkflow)

	add(srv, "create_subworkflow",
		"Create a subworkflow (step / vertical column) inside a workflow. Features live at the intersection of a subworkflow and a milestone.",
		func(a createSubWorkflowArgs) string { return a.WorkspaceID }, mcpCreateSubWorkflow)

	add(srv, "create_feature",
		"Create a new feature (story card) inside the intersection of a subworkflow and a milestone. Server mints the UUID and positions the card at the end of the target cell.",
		func(a createFeatureArgs) string { return a.WorkspaceID }, mcpCreateFeature)

	add(srv, "rename_feature",
		"Change the title of an existing feature card.",
		func(a renameFeatureArgs) string { return a.WorkspaceID }, mcpRenameFeature)

	add(srv, "update_feature_description",
		"Replace the markdown description of a feature card.",
		func(a updateFeatureDescArgs) string { return a.WorkspaceID }, mcpUpdateFeatureDescription)

	add(srv, "move_feature",
		"Move a feature to a different milestone and/or subworkflow, at a given 0-based index within the target cell. Server computes the lexorank.",
		func(a moveFeatureArgs) string { return a.WorkspaceID }, mcpMoveFeature)

	add(srv, "delete_feature",
		"Remove a feature card.",
		func(a deleteFeatureArgs) string { return a.WorkspaceID }, mcpDeleteFeature)

	add(srv, "update_feature",
		"Update a feature card in one call, applying only the fields you provide (omit a field to leave it unchanged). Optional fields: title, description, color (WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK), status (OPEN or CLOSED), and movement (to_milestone_id, to_subworkflow_id, index). When moving, omit index to append to the end of the target cell; the server computes the lexorank. With no optional fields set, returns the current feature unchanged.",
		func(a updateFeatureArgs) string { return a.WorkspaceID }, mcpUpdateFeature)

	add(srv, "bulk_create_features",
		"Create many feature cards in one call (max 100). Each item targets a subworkflow+milestone intersection with a title. Best-effort: each item succeeds or fails independently; the response lists per-item {index, ok, id, error} in input order. An empty or oversized batch is rejected with no writes.",
		func(a bulkCreateFeaturesArgs) string { return a.WorkspaceID }, mcpBulkCreateFeatures)

	add(srv, "bulk_update_features",
		"Update many feature cards in one call (max 100). Each item carries a feature_id plus only the fields to change (title, description, color, status, to_milestone_id, to_subworkflow_id, index) -- same partial semantics as update_feature. Best-effort: per-item {index, ok, id, error} in input order; an empty or oversized batch is rejected with no writes.",
		func(a bulkUpdateFeaturesArgs) string { return a.WorkspaceID }, mcpBulkUpdateFeatures)

	add(srv, "add_comment",
		"Add a comment to a feature card. Body is markdown.",
		func(a addCommentArgs) string { return a.WorkspaceID }, mcpAddComment)

	add(srv, "set_feature_color",
		"Set the color band on a feature (story card). Use color to highlight risk, theme, or status. Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
		func(a setFeatureColorArgs) string { return a.WorkspaceID }, mcpSetFeatureColor)

	add(srv, "set_milestone_color",
		"Set the color of a milestone (release row). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
		func(a setMilestoneColorArgs) string { return a.WorkspaceID }, mcpSetMilestoneColor)

	add(srv, "set_workflow_color",
		"Set the color of a workflow (activity column). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
		func(a setWorkflowColorArgs) string { return a.WorkspaceID }, mcpSetWorkflowColor)

	add(srv, "set_subworkflow_color",
		"Set the color of a subworkflow (step). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
		func(a setSubWorkflowColorArgs) string { return a.WorkspaceID }, mcpSetSubWorkflowColor)

	add(srv, "move_milestone",
		"Reorder a milestone (release column) within its project. Index is 0-based among siblings.",
		func(a moveMilestoneArgs) string { return a.WorkspaceID }, mcpMoveMilestone)

	add(srv, "move_workflow",
		"Reorder a workflow (activity row) within its project. Index is 0-based among siblings.",
		func(a moveWorkflowArgs) string { return a.WorkspaceID }, mcpMoveWorkflow)

	add(srv, "move_subworkflow",
		"Move a subworkflow to another (or the same) workflow at a 0-based index. Use to reorder columns or graft a step onto a different activity.",
		func(a moveSubWorkflowArgs) string { return a.WorkspaceID }, mcpMoveSubWorkflow)

	add(srv, "set_feature_status",
		"Close or re-open a feature card. Pass status: \"OPEN\" or \"CLOSED\". Closed cards stay on the board but render as done.",
		func(a setFeatureStatusArgs) string { return a.WorkspaceID }, mcpSetFeatureStatus)

	add(srv, "set_milestone_status",
		"Close or re-open a milestone (release column). Pass status: \"OPEN\" or \"CLOSED\".",
		func(a setMilestoneStatusArgs) string { return a.WorkspaceID }, mcpSetMilestoneStatus)

	add(srv, "set_workflow_status",
		"Close or re-open a workflow (activity row). Pass status: \"OPEN\" or \"CLOSED\".",
		func(a setWorkflowStatusArgs) string { return a.WorkspaceID }, mcpSetWorkflowStatus)

	add(srv, "set_subworkflow_status",
		"Close or re-open a subworkflow (step / column). Pass status: \"OPEN\" or \"CLOSED\".",
		func(a setSubWorkflowStatusArgs) string { return a.WorkspaceID }, mcpSetSubWorkflowStatus)

	add(srv, "create_persona",
		"Create a persona inside a project. Personas describe target users. Avatar must be one of avatar00..avatar08. If workflow_id is supplied, also attaches the persona to that workflow.",
		func(a createPersonaArgs) string { return a.WorkspaceID }, mcpCreatePersona)

	add(srv, "update_persona",
		"Edit an existing persona's avatar/name/role/description.",
		func(a updatePersonaArgs) string { return a.WorkspaceID }, mcpUpdatePersona)

	add(srv, "delete_persona",
		"Delete a persona. Cascade-removes all workflow attachments for that persona.",
		func(a deletePersonaArgs) string { return a.WorkspaceID }, mcpDeletePersona)

	add(srv, "attach_persona_to_workflow",
		"Attach an existing persona to a workflow (the activity row). Same persona can be attached to multiple workflows.",
		func(a attachPersonaArgs) string { return a.WorkspaceID }, mcpAttachPersonaToWorkflow)

	add(srv, "detach_persona_from_workflow",
		"Remove a workflow-persona link. Pass the workflow_persona_id (NOT the persona_id) -- find it in get_board.workflowPersonas[].id.",
		func(a detachPersonaArgs) string { return a.WorkspaceID }, mcpDetachPersonaFromWorkflow)

	return srv
}

// add is a tiny generic helper that hides the AddTool + withService combo.
// Keeps the registration block above readable as a near-table.
func add[In, Out any](
	srv *mcpsdk.Server,
	name string,
	description string,
	resolveWS func(In) string,
	handler func(context.Context, Service, In) (Out, error),
) {
	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        name,
		Description: description,
	}, withService(resolveWS, handler))
}

// mcpHTTPHandler returns an http.Handler that serves the MCP Streamable HTTP
// protocol. The same Server instance is reused across all sessions.
func mcpHTTPHandler() http.Handler {
	server := buildMCPServer()
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{
			Stateless: true,
		},
	)
}
