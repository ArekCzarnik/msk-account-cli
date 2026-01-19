package output

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/czarnik/msk-account-cli/internal/kafka"
	"github.com/olekukonko/tablewriter"
)

func PrintJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func FormatAndPrintACLs(acls []kafka.ACLEntry, format string) error {
	if format == "json" {
		return PrintJSON(acls)
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("ResourceType", "Name", "Pattern", "Principal", "Host", "Operation", "Permission")
	for _, a := range acls {
		_ = table.Append(a.ResourceType, a.ResourceName, a.ResourcePattern, a.Principal, a.Host, a.Operation, a.Permission)
	}
	table.Render()
	return nil
}

func FormatAndPrintConsumerGroups(groups []string, format string) error {
	sort.Strings(groups)
	if format == "json" {
		return PrintJSON(groups)
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("GroupID")
	for _, g := range groups {
		_ = table.Append(g)
	}
	table.Render()
	return nil
}

func FormatAndPrintConsumerGroupDescription(d *kafka.ConsumerGroupDescription, format string) error {
	if format == "json" {
		return PrintJSON(d)
	}
	fmt.Fprintf(os.Stdout, "Group: %s\nState: %s\n", d.GroupID, d.State)
	if len(d.Members) == 0 {
		fmt.Fprintln(os.Stdout, "Members: 0")
		return nil
	}
	fmt.Fprintln(os.Stdout, "Members:")
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("MemberID", "ClientID", "ClientHost")
	for _, m := range d.Members {
		_ = table.Append(m.MemberID, m.ClientID, m.ClientHost)
	}
	table.Render()
	return nil
}

func FormatAndPrintGroupDeleteResults(results map[string]error, format string) error {
	type row struct {
		GroupID string `json:"groupId"`
		Error   string `json:"error,omitempty"`
	}
	rows := make([]row, 0, len(results))
	for g, err := range results {
		var e string
		if err != nil {
			e = err.Error()
		}
		rows = append(rows, row{GroupID: g, Error: e})
	}
	if format == "json" {
		return PrintJSON(rows)
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("GroupID", "Error")
	for _, r := range rows {
		_ = table.Append(r.GroupID, r.Error)
	}
	table.Render()
	return nil
}

// SecretRow represents a single secret for listing output.
type SecretRow struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

func FormatAndPrintSecrets(rows []SecretRow, format string) error {
	if format == "json" {
		return PrintJSON(rows)
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("Name", "ARN")
	for _, r := range rows {
		_ = table.Append(r.Name, r.ARN)
	}
	table.Render()
	return nil
}

// ClusterRow for MSK clusters
type ClusterRow struct {
	Name  string `json:"name"`
	ARN   string `json:"arn"`
	State string `json:"state,omitempty"`
	Type  string `json:"type,omitempty"`
}

// FormatAndPrintClusters prints clusters. For JSON, all fields are included.
// For table, columns controls which fields to show (defaults: name,arn).
func FormatAndPrintClusters(rows []ClusterRow, format string, columns []string) error {
	if format == "json" {
		return PrintJSON(rows)
	}
	// default columns
	if len(columns) == 0 {
		columns = []string{"name", "arn"}
	}
	// Header
	hdr := make([]string, 0, len(columns))
	for _, c := range columns {
		hdr = append(hdr, strings.ToUpper(c))
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header(toAny(hdr)...)
	for _, r := range rows {
		vals := make([]string, 0, len(columns))
		for _, c := range columns {
			switch strings.ToLower(strings.TrimSpace(c)) {
			case "name":
				vals = append(vals, r.Name)
			case "arn":
				vals = append(vals, r.ARN)
			case "state":
				vals = append(vals, r.State)
			case "type":
				vals = append(vals, r.Type)
			default:
				vals = append(vals, "")
			}
		}
		_ = table.Append(toAny(vals)...)
	}
	table.Render()
	return nil
}

func toAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Brokers
type BrokerRow struct {
	ID        string   `json:"id"`
	Endpoints []string `json:"endpoints"`
}

func FormatAndPrintBrokers(rows []BrokerRow, format string) error {
	if format == "json" {
		return PrintJSON(rows)
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.Header("ID", "Endpoints")
	for _, r := range rows {
		_ = table.Append(r.ID, strings.Join(r.Endpoints, ","))
	}
	table.Render()
	return nil
}
