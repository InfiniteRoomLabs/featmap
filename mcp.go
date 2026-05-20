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

// Tool arg types -- one per tool, drives JSON schema generation.

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

// withService is a tool-handler decorator that pulls the per-request Service
// out of context and short-circuits if auth middleware didn't populate it.
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

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_workspaces",
		Description: "List all workspaces the authenticated account belongs to. Call first to discover workspace IDs.",
	}, withService(
		func(emptyArgs) string { return "" },
		func(ctx context.Context, s Service, _ emptyArgs) (struct {
			Workspaces []*Workspace `json:"workspaces"`
		}, error) {
			return struct {
				Workspaces []*Workspace `json:"workspaces"`
			}{Workspaces: s.GetWorkspaces()}, nil
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "list_projects",
		Description: "List all projects in a workspace.",
	}, withService(
		func(a workspaceArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, _ workspaceArgs) (struct {
			Projects []*Project `json:"projects"`
		}, error) {
			return struct {
				Projects []*Project `json:"projects"`
			}{Projects: s.GetProjects()}, nil
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "get_board",
		Description: "Fetch the entire story map for a project: milestones, workflows, subworkflows, features, feature comments, personas, workflow personas.",
	}, withService(
		func(a getBoardArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a getBoardArgs) (map[string]any, error) {
			project := s.GetProject(a.ProjectID)
			if project == nil {
				return nil, errors.New("project not found")
			}
			return map[string]any{
				"project":          project,
				"milestones":       s.GetMilestonesByProject(a.ProjectID),
				"workflows":        s.GetWorkflowsByProject(a.ProjectID),
				"subWorkflows":     s.GetSubWorkflowsByProject(a.ProjectID),
				"features":         s.GetFeaturesByProject(a.ProjectID),
				"featureComments":  s.GetFeatureCommentsByProject(a.ProjectID),
				"personas":         s.GetPersonasByProject(a.ProjectID),
				"workflowPersonas": s.GetWorkflowPersonasByProject(a.ProjectID),
			}, nil
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_project",
		Description: "Create a new project in a workspace. A project owns the entire story map (milestones, workflows, subworkflows, features).",
	}, withService(
		func(a createProjectArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createProjectArgs) (*Project, error) {
			return s.CreateProjectWithID(newUUID(), a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_milestone",
		Description: "Create a milestone (release column, horizontal) inside a project. Milestones group features by planned release.",
	}, withService(
		func(a createMilestoneArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createMilestoneArgs) (*Milestone, error) {
			return s.CreateMilestoneWithID(newUUID(), a.ProjectID, a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_workflow",
		Description: "Create a workflow (activity, top row) inside a project. Workflows group subworkflows.",
	}, withService(
		func(a createWorkflowArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createWorkflowArgs) (*Workflow, error) {
			return s.CreateWorkflowWithID(newUUID(), a.ProjectID, a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_subworkflow",
		Description: "Create a subworkflow (step / vertical column) inside a workflow. Features live at the intersection of a subworkflow and a milestone.",
	}, withService(
		func(a createSubWorkflowArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createSubWorkflowArgs) (*SubWorkflow, error) {
			return s.CreateSubWorkflowWithID(newUUID(), a.WorkflowID, a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_feature",
		Description: "Create a new feature (story card) inside the intersection of a subworkflow and a milestone. Server mints the UUID and positions the card at the end of the target cell.",
	}, withService(
		func(a createFeatureArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createFeatureArgs) (*Feature, error) {
			id := newUUID()
			return s.CreateFeatureWithID(id, a.SubWorkflowID, a.MilestoneID, a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "rename_feature",
		Description: "Change the title of an existing feature card.",
	}, withService(
		func(a renameFeatureArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a renameFeatureArgs) (*Feature, error) {
			return s.RenameFeature(a.FeatureID, a.Title)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_feature_description",
		Description: "Replace the markdown description of a feature card.",
	}, withService(
		func(a updateFeatureDescArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a updateFeatureDescArgs) (*Feature, error) {
			return s.UpdateFeatureDescription(a.FeatureID, a.Description)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "move_feature",
		Description: "Move a feature to a different milestone and/or subworkflow, at a given 0-based index within the target cell. Server computes the lexorank.",
	}, withService(
		func(a moveFeatureArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a moveFeatureArgs) (*Feature, error) {
			return s.MoveFeature(a.FeatureID, a.ToMilestoneID, a.ToSubWorkflowID, a.Index)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_feature",
		Description: "Remove a feature card.",
	}, withService(
		func(a deleteFeatureArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a deleteFeatureArgs) (struct {
			OK bool `json:"ok"`
		}, error) {
			if err := s.DeleteFeature(a.FeatureID); err != nil {
				return struct {
					OK bool `json:"ok"`
				}{OK: false}, err
			}
			return struct {
				OK bool `json:"ok"`
			}{OK: true}, nil
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "add_comment",
		Description: "Add a comment to a feature card. Body is markdown.",
	}, withService(
		func(a addCommentArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a addCommentArgs) (*FeatureComment, error) {
			return s.CreateFeatureCommentWithID(newUUID(), a.FeatureID, a.Body)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_feature_color",
		Description: "Set the color band on a feature (story card). Use color to highlight risk, theme, or status. Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
	}, withService(
		func(a setFeatureColorArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setFeatureColorArgs) (*Feature, error) {
			return s.ChangeColorOnFeature(a.FeatureID, a.Color)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_milestone_color",
		Description: "Set the color of a milestone (release row). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
	}, withService(
		func(a setMilestoneColorArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setMilestoneColorArgs) (*Milestone, error) {
			return s.ChangeColorOnMilestone(a.MilestoneID, a.Color)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_workflow_color",
		Description: "Set the color of a workflow (activity column). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
	}, withService(
		func(a setWorkflowColorArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setWorkflowColorArgs) (*Workflow, error) {
			return s.ChangeColorOnWorkflow(a.WorkflowID, a.Color)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_subworkflow_color",
		Description: "Set the color of a subworkflow (step). Valid colors: WHITE, GREY, RED, ORANGE, YELLOW, GREEN, TEAL, BLUE, INDIGO, PURPLE, PINK.",
	}, withService(
		func(a setSubWorkflowColorArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setSubWorkflowColorArgs) (*SubWorkflow, error) {
			return s.ChangeColorOnSubWorkflow(a.SubWorkflowID, a.Color)
		},
	))

	// Structural move tools. Move semantics mirror move_feature: server
	// computes the lexorank for the target index, callers stay ignorant of
	// rank strings.

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "move_milestone",
		Description: "Reorder a milestone (release column) within its project. Index is 0-based among siblings.",
	}, withService(
		func(a moveMilestoneArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a moveMilestoneArgs) (*Milestone, error) {
			return s.MoveMilestone(a.MilestoneID, a.Index)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "move_workflow",
		Description: "Reorder a workflow (activity row) within its project. Index is 0-based among siblings.",
	}, withService(
		func(a moveWorkflowArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a moveWorkflowArgs) (*Workflow, error) {
			return s.MoveWorkflow(a.WorkflowID, a.Index)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "move_subworkflow",
		Description: "Move a subworkflow to another (or the same) workflow at a 0-based index. Use to reorder columns or graft a step onto a different activity.",
	}, withService(
		func(a moveSubWorkflowArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a moveSubWorkflowArgs) (*SubWorkflow, error) {
			return s.MoveSubWorkflow(a.SubWorkflowID, a.ToWorkflowID, a.Index)
		},
	))

	// Status tools. Each is a single tool per entity that toggles via the
	// `status` arg, so close + reopen share a name.

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_feature_status",
		Description: "Close or re-open a feature card. Pass status: \"OPEN\" or \"CLOSED\". Closed cards stay on the board but render as done.",
	}, withService(
		func(a setFeatureStatusArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setFeatureStatusArgs) (*Feature, error) {
			switch a.Status {
			case "OPEN":
				return s.OpenFeature(a.FeatureID)
			case "CLOSED":
				return s.CloseFeature(a.FeatureID)
			default:
				return nil, errors.New("status must be OPEN or CLOSED")
			}
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_milestone_status",
		Description: "Close or re-open a milestone (release column). Pass status: \"OPEN\" or \"CLOSED\".",
	}, withService(
		func(a setMilestoneStatusArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setMilestoneStatusArgs) (*Milestone, error) {
			switch a.Status {
			case "OPEN":
				return s.OpenMilestone(a.MilestoneID)
			case "CLOSED":
				return s.CloseMilestone(a.MilestoneID)
			default:
				return nil, errors.New("status must be OPEN or CLOSED")
			}
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_workflow_status",
		Description: "Close or re-open a workflow (activity row). Pass status: \"OPEN\" or \"CLOSED\".",
	}, withService(
		func(a setWorkflowStatusArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setWorkflowStatusArgs) (*Workflow, error) {
			switch a.Status {
			case "OPEN":
				return s.OpenWorkflow(a.WorkflowID)
			case "CLOSED":
				return s.CloseWorkflow(a.WorkflowID)
			default:
				return nil, errors.New("status must be OPEN or CLOSED")
			}
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "set_subworkflow_status",
		Description: "Close or re-open a subworkflow (step / column). Pass status: \"OPEN\" or \"CLOSED\".",
	}, withService(
		func(a setSubWorkflowStatusArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a setSubWorkflowStatusArgs) (*SubWorkflow, error) {
			switch a.Status {
			case "OPEN":
				return s.OpenSubWorkflow(a.SubWorkflowID)
			case "CLOSED":
				return s.CloseSubWorkflow(a.SubWorkflowID)
			default:
				return nil, errors.New("status must be OPEN or CLOSED")
			}
		},
	))

	// Persona tools. Personas live at the project level but get attached to
	// workflows via the workflow_personas table -- they describe WHO an
	// activity (workflow) is for. Cards have no direct persona link.

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "create_persona",
		Description: "Create a persona inside a project. Personas describe target users. Avatar must be one of avatar00..avatar08. If workflow_id is supplied, also attaches the persona to that workflow.",
	}, withService(
		func(a createPersonaArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a createPersonaArgs) (*Persona, error) {
			return s.CreatePersonaWithID(newUUID(), a.ProjectID, a.Avatar, a.Name, a.Role, a.Description, a.WorkflowID, newUUID())
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "update_persona",
		Description: "Edit an existing persona's avatar/name/role/description.",
	}, withService(
		func(a updatePersonaArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a updatePersonaArgs) (*Persona, error) {
			return s.UpdatePersona(a.PersonaID, a.Avatar, a.Name, a.Role, a.Description)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "delete_persona",
		Description: "Delete a persona. Cascade-removes all workflow attachments for that persona.",
	}, withService(
		func(a deletePersonaArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a deletePersonaArgs) (struct {
			OK bool `json:"ok"`
		}, error) {
			if err := s.DeletePersona(a.PersonaID); err != nil {
				return struct {
					OK bool `json:"ok"`
				}{OK: false}, err
			}
			return struct {
				OK bool `json:"ok"`
			}{OK: true}, nil
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "attach_persona_to_workflow",
		Description: "Attach an existing persona to a workflow (the activity row). Same persona can be attached to multiple workflows.",
	}, withService(
		func(a attachPersonaArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a attachPersonaArgs) (*WorkflowPersona, error) {
			return s.CreateWorkflowPersonaWithID(newUUID(), a.WorkflowID, a.PersonaID)
		},
	))

	mcpsdk.AddTool(srv, &mcpsdk.Tool{
		Name:        "detach_persona_from_workflow",
		Description: "Remove a workflow-persona link. Pass the workflow_persona_id (NOT the persona_id) -- find it in get_board.workflowPersonas[].id.",
	}, withService(
		func(a detachPersonaArgs) string { return a.WorkspaceID },
		func(ctx context.Context, s Service, a detachPersonaArgs) (struct {
			OK bool `json:"ok"`
		}, error) {
			if err := s.DeleteWorkflowPersona(a.WorkflowPersonaID); err != nil {
				return struct {
					OK bool `json:"ok"`
				}{OK: false}, err
			}
			return struct {
				OK bool `json:"ok"`
			}{OK: true}, nil
		},
	))

	return srv
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
