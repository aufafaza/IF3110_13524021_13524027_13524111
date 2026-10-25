package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type StatementPlan struct {
	Index             int      `json:"index"`
	SQL               string   `json:"sql"`
	IsExplainable     bool     `json:"is_explainable"`
	Mode              string   `json:"mode"`
	NodeTypes         []string `json:"node_types,omitempty"`
	ScanTypes         []string `json:"scan_types,omitempty"`
	IndexesUsed       []string `json:"indexes_used,omitempty"`
	JoinStrategies    []string `json:"join_strategies,omitempty"`
	StartupCost       float64  `json:"startup_cost"`
	TotalCost         float64  `json:"total_cost"`
	PlanRows          int64    `json:"plan_rows"`
	ActualTotalTimeMs *float64 `json:"actual_total_time_ms,omitempty"`
	ActualRows        *int64   `json:"actual_rows,omitempty"`
	RawPlan           string   `json:"raw_plan"`
}

type ExecutionPlanReport struct {
	Analyze    bool            `json:"analyze"`
	Statements []StatementPlan `json:"statements"`
}

type PlanAnalyzer interface {
	AnalyzePlan(ctx context.Context, databaseID int64, sql string, analyze bool) (ExecutionPlanReport, error)
}

type pgExplainRoot struct {
	Plan          pgExplainNode `json:"Plan"`
	PlanningTime  *float64      `json:"Planning Time,omitempty"`
	ExecutionTime *float64      `json:"Execution Time,omitempty"`
}

type pgExplainNode struct {
	NodeType        string          `json:"Node Type"`
	JoinType        string          `json:"Join Type,omitempty"`
	RelationName    string          `json:"Relation Name,omitempty"`
	IndexName       string          `json:"Index Name,omitempty"`
	StartupCost     float64         `json:"Startup Cost"`
	TotalCost       float64         `json:"Total Cost"`
	PlanRows        int64           `json:"Plan Rows"`
	ActualTotalTime *float64        `json:"Actual Total Time,omitempty"`
	ActualRows      *int64          `json:"Actual Rows,omitempty"`
	ActualLoops     *int64          `json:"Actual Loops,omitempty"`
	Filter          string          `json:"Filter,omitempty"`
	IndexCond       string          `json:"Index Cond,omitempty"`
	HashCond        string          `json:"Hash Cond,omitempty"`
	Plans           []pgExplainNode `json:"Plans,omitempty"`
}

func IsExplainableStatement(sql string) bool {
	stmts := splitAndTokenize(sql)
	if len(stmts) == 0 || len(stmts[0].tokens) == 0 {
		return false
	}
	tokens := stmts[0].tokens
	switch tokens[0] {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "VALUES", "WITH", "TABLE":
		return true
	case "CREATE":
		for i := 1; i < len(tokens); i++ {
			if tokens[i] == "AS" {
				return true
			}
			if tokens[i] == "(" {
				break
			}
		}
	}
	return false
}

func (e *TargetPostgresExecutor) AnalyzePlan(ctx context.Context, databaseID int64, sql string, analyze bool) (ExecutionPlanReport, error) {
	if err := ValidateSQL(sql); err != nil {
		return ExecutionPlanReport{}, err
	}

	pool, err := e.provider.Pool(ctx, databaseID)
	if err != nil {
		return ExecutionPlanReport{}, fmt.Errorf("migration: target pool: %w", err)
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return ExecutionPlanReport{}, fmt.Errorf("migration: acquire target connection: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return ExecutionPlanReport{}, fmt.Errorf("migration: begin explain tx: %w", err)
	}
	defer func() {
		rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rbCtx)
	}()

	stmts := SplitStatements(sql)
	report := ExecutionPlanReport{
		Analyze:    analyze,
		Statements: make([]StatementPlan, 0, len(stmts)),
	}

	for idx, stmt := range stmts {
		if IsExplainableStatement(stmt) {
			var jsonRaw string
			var explainQuery string
			mode := "EXPLAIN"
			if analyze {
				mode = "EXPLAIN ANALYZE"
				explainQuery = "EXPLAIN (ANALYZE TRUE, COSTS TRUE, TIMING TRUE, FORMAT JSON) " + stmt
			} else {
				explainQuery = "EXPLAIN (COSTS TRUE, FORMAT JSON) " + stmt
			}

			if err := tx.QueryRow(ctx, explainQuery).Scan(&jsonRaw); err != nil {
				return ExecutionPlanReport{}, fmt.Errorf("migration: explain statement %d: %w", idx+1, err)
			}

			plan, err := parsePGExplainJSON(idx+1, stmt, mode, jsonRaw)
			if err != nil {
				return ExecutionPlanReport{}, err
			}

			if !analyze {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return ExecutionPlanReport{}, fmt.Errorf("migration: simulate statement %d: %w", idx+1, err)
				}
			}

			report.Statements = append(report.Statements, plan)
			continue
		}

		start := time.Now()
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return ExecutionPlanReport{}, fmt.Errorf("migration: validate DDL statement %d: %w", idx+1, err)
		}
		elapsedMs := float64(time.Since(start).Microseconds()) / 1000.0

		var actualTime *float64
		if analyze {
			actualTime = &elapsedMs
		}

		report.Statements = append(report.Statements, StatementPlan{
			Index:             idx + 1,
			SQL:               stmt,
			IsExplainable:     false,
			Mode:              "DDL SANDBOX",
			NodeTypes:         []string{"DDL Statement"},
			ActualTotalTimeMs: actualTime,
			RawPlan:           "DDL statement verified in sandbox transaction (rolled back automatically)",
		})
	}

	return report, nil
}

func parsePGExplainJSON(index int, stmt, mode, jsonRaw string) (StatementPlan, error) {
	var roots []pgExplainRoot
	if err := json.Unmarshal([]byte(jsonRaw), &roots); err != nil || len(roots) == 0 {
		return StatementPlan{}, fmt.Errorf("migration: parse explain json: %w", err)
	}

	root := roots[0]
	var nodeTypes, scanTypes, indexesUsed, joinStrategies []string
	seenNodes := make(map[string]bool)
	seenScans := make(map[string]bool)
	seenIndexes := make(map[string]bool)
	seenJoins := make(map[string]bool)

	var textLines []string
	var walk func(n pgExplainNode, depth int)
	walk = func(n pgExplainNode, depth int) {
		if n.NodeType != "" && !seenNodes[n.NodeType] {
			seenNodes[n.NodeType] = true
			nodeTypes = append(nodeTypes, n.NodeType)
		}
		if strings.Contains(n.NodeType, "Scan") && !seenScans[n.NodeType] {
			seenScans[n.NodeType] = true
			scanTypes = append(scanTypes, n.NodeType)
		}
		if n.IndexName != "" && !seenIndexes[n.IndexName] {
			seenIndexes[n.IndexName] = true
			indexesUsed = append(indexesUsed, n.IndexName)
		}
		if (strings.Contains(n.NodeType, "Join") || n.NodeType == "Nested Loop") && !seenJoins[n.NodeType] {
			seenJoins[n.NodeType] = true
			label := n.NodeType
			if n.JoinType != "" {
				label = n.NodeType + " (" + n.JoinType + ")"
			}
			joinStrategies = append(joinStrategies, label)
		}

		indent := strings.Repeat("  ", depth)
		prefix := ""
		if depth > 0 {
			prefix = "->  "
		}
		target := ""
		if n.RelationName != "" {
			target += " on " + n.RelationName
		}
		if n.IndexName != "" {
			target += " using " + n.IndexName
		}
		line := fmt.Sprintf("%s%s%s%s  (cost=%.2f..%.2f rows=%d)",
			indent, prefix, n.NodeType, target, n.StartupCost, n.TotalCost, n.PlanRows)
		if n.ActualTotalTime != nil && n.ActualRows != nil {
			line += fmt.Sprintf(" (actual time=%.3f ms rows=%d)", *n.ActualTotalTime, *n.ActualRows)
		}
		textLines = append(textLines, line)

		if n.IndexCond != "" {
			textLines = append(textLines, fmt.Sprintf("%s      Index Cond: %s", indent, n.IndexCond))
		}
		if n.Filter != "" {
			textLines = append(textLines, fmt.Sprintf("%s      Filter: %s", indent, n.Filter))
		}
		if n.HashCond != "" {
			textLines = append(textLines, fmt.Sprintf("%s      Hash Cond: %s", indent, n.HashCond))
		}

		for _, child := range n.Plans {
			walk(child, depth+1)
		}
	}

	walk(root.Plan, 0)
	if root.PlanningTime != nil {
		textLines = append(textLines, fmt.Sprintf("Planning Time: %.3f ms", *root.PlanningTime))
	}
	if root.ExecutionTime != nil {
		textLines = append(textLines, fmt.Sprintf("Execution Time: %.3f ms", *root.ExecutionTime))
	}

	var actualTime *float64
	if root.ExecutionTime != nil {
		actualTime = root.ExecutionTime
	} else if root.Plan.ActualTotalTime != nil {
		actualTime = root.Plan.ActualTotalTime
	}

	return StatementPlan{
		Index:             index,
		SQL:               stmt,
		IsExplainable:     true,
		Mode:              mode,
		NodeTypes:         nodeTypes,
		ScanTypes:         scanTypes,
		IndexesUsed:       indexesUsed,
		JoinStrategies:    joinStrategies,
		StartupCost:       root.Plan.StartupCost,
		TotalCost:         root.Plan.TotalCost,
		PlanRows:          root.Plan.PlanRows,
		ActualTotalTimeMs: actualTime,
		ActualRows:        root.Plan.ActualRows,
		RawPlan:           strings.Join(textLines, "\n"),
	}, nil
}
