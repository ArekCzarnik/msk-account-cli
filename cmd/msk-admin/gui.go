package main

import (
	"context"
	"strings"
	"time"

	iaws "github.com/czarnik/msk-account-cli/internal/aws"
	"github.com/czarnik/msk-account-cli/internal/config"
	"github.com/czarnik/msk-account-cli/internal/kafka"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
)

// cmdGUI starts a tview-based UI that ruft die bestehenden Funktionen auf
func cmdGUI() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gui",
		Short: "Start TUI (tview) to browse outputs of commands",
		RunE: func(cmd *cobra.Command, args []string) error {
			app := tview.NewApplication()

			header := tview.NewTextView().SetTextAlign(tview.AlignLeft)
			header.SetText("MSK Admin GUI — Press q to quit")

			status := tview.NewTextView().SetDynamicColors(true)
			setStatus := func(s string) { app.QueueUpdateDraw(func() { status.SetText(s) }) }

			table := tview.NewTable().SetBorders(true)
			text := tview.NewTextView().SetDynamicColors(true)

			pages := tview.NewPages().AddPage("table", table, true, true).AddPage("text", text, true, false)

			menu := tview.NewList().ShowSecondaryText(false)
			menu.SetBorder(true).SetTitle("Menu")

			// helpers
			showText := func(s string) {
				app.QueueUpdateDraw(func() {
					text.SetText(s)
					pages.SwitchToPage("text")
				})
			}
			showTable := func(headers []string, rows [][]string) {
				app.QueueUpdateDraw(func() {
					table.Clear()
					for c, h := range headers {
						table.SetCell(0, c, tview.NewTableCell(h).SetAttributes(tcell.AttrBold))
					}
					for r, row := range rows {
						for c, v := range row {
							table.SetCell(r+1, c, tview.NewTableCell(v))
						}
					}
					pages.SwitchToPage("table")
				})
			}
			runAsync := func(fn func() error) {
				go func() {
					setStatus("[yellow]Loading…")
					err := fn()
					if err != nil {
						showText("[red]" + err.Error())
						setStatus("[red]Error")
					} else {
						setStatus("[green]Done")
					}
				}()
			}

			// Prefill defaults from config
			_, _, _ = config.LoadAppConfig("")
			defRegion := config.GetDefaultRegion()
			defBrokers := strings.Join(config.GetDefaultBrokers(), ",")
			defUser := config.GetDefaultSASLUsername()
			defPass := config.GetDefaultSASLPassword()

			// Item: Accounts -> List secrets
			menu.AddItem("Accounts: List Secrets", "", '1', func() {
				form := tview.NewForm().AddInputField("Region", defRegion, 40, nil, nil)
				form.AddButton("Run", func() {
					region := strings.TrimSpace(form.GetFormItemByLabel("Region").(*tview.InputField).GetText())
					if region == "" {
						showText("Region required")
						return
					}
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						secs, err := awsListMSKSecrets(ctx, region)
						if err != nil {
							return err
						}
						rows := make([][]string, 0, len(secs))
						for _, s := range secs {
							rows = append(rows, []string{s.Name, s.ARN})
						}
						showTable([]string{"Name", "ARN"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("Accounts: List Secrets")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Item: MSK -> List clusters
			menu.AddItem("MSK: List Clusters", "", '2', func() {
				form := tview.NewForm().AddInputField("Region", defRegion, 40, nil, nil).AddInputField("Name Prefix", "", 40, nil, nil)
				form.AddButton("Run", func() {
					region := strings.TrimSpace(form.GetFormItemByLabel("Region").(*tview.InputField).GetText())
					namePrefix := strings.TrimSpace(form.GetFormItemByLabel("Name Prefix").(*tview.InputField).GetText())
					if region == "" {
						showText("Region required")
						return
					}
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						cls, err := awsListClusters(ctx, region, namePrefix)
						if err != nil {
							return err
						}
						rows := make([][]string, 0, len(cls))
						for _, c := range cls {
							rows = append(rows, []string{c.Name, c.ARN, c.State, c.Type})
						}
						showTable([]string{"Name", "ARN", "State", "Type"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("MSK: List Clusters")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Item: MSK -> List brokers
			menu.AddItem("MSK: List Brokers", "", '3', func() {
				form := tview.NewForm().AddInputField("Region", defRegion, 40, nil, nil).AddInputField("Cluster ARN", config.GetDefaultClusterARN(), 80, nil, nil)
				form.AddButton("Run", func() {
					region := strings.TrimSpace(form.GetFormItemByLabel("Region").(*tview.InputField).GetText())
					cluster := strings.TrimSpace(form.GetFormItemByLabel("Cluster ARN").(*tview.InputField).GetText())
					if region == "" || cluster == "" {
						showText("Region and Cluster ARN required")
						return
					}
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						brs, err := awsListBrokers(ctx, region, cluster)
						if err != nil {
							return err
						}
						rows := make([][]string, 0, len(brs))
						for _, b := range brs {
							rows = append(rows, []string{b.ID, strings.Join(b.Endpoints, ",")})
						}
						showTable([]string{"ID", "Endpoints"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("MSK: List Brokers")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Item: ACL -> List
			menu.AddItem("ACL: List", "", '4', func() {
				form := tview.NewForm().
					AddInputField("Brokers", defBrokers, 80, nil, nil).
					AddInputField("SASL Username", defUser, 40, nil, nil).
					AddPasswordField("SASL Password", defPass, 40, '*', nil).
					AddInputField("Secret ARN", "", 80, nil, nil).
					AddInputField("Region (for Secret)", defRegion, 40, nil, nil).
					AddInputField("Resource Type", "", 20, nil, nil).
					AddInputField("Resource Name", "", 40, nil, nil).
					AddInputField("Principal", "", 40, nil, nil).
					AddInputField("Operation", "", 20, nil, nil)
				form.AddButton("Run", func() {
					brokers := strings.TrimSpace(form.GetFormItemByLabel("Brokers").(*tview.InputField).GetText())
					u := strings.TrimSpace(form.GetFormItemByLabel("SASL Username").(*tview.InputField).GetText())
					p := strings.TrimSpace(form.GetFormItemByLabel("SASL Password").(*tview.InputField).GetText())
					sarn := strings.TrimSpace(form.GetFormItemByLabel("Secret ARN").(*tview.InputField).GetText())
					reg := strings.TrimSpace(form.GetFormItemByLabel("Region (for Secret)").(*tview.InputField).GetText())
					rtype := strings.TrimSpace(form.GetFormItemByLabel("Resource Type").(*tview.InputField).GetText())
					rname := strings.TrimSpace(form.GetFormItemByLabel("Resource Name").(*tview.InputField).GetText())
					princ := strings.TrimSpace(form.GetFormItemByLabel("Principal").(*tview.InputField).GetText())
					oper := strings.TrimSpace(form.GetFormItemByLabel("Operation").(*tview.InputField).GetText())
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						if sarn == "" {
							if strings.TrimSpace(u) == "" {
								u = config.GetDefaultSASLUsername()
							}
							if strings.TrimSpace(p) == "" {
								p = config.GetDefaultSASLPassword()
							}
						}
						ac := kafka.AuthConfig{Brokers: splitNotEmpty(brokers), SASLUsername: u, SASLPassword: p, SCRAMMechanism: config.GetDefaultSCRAM()}
						if sarn != "" {
							// fetch via secret
							sec, err := awsGetSecret(ctx, iaws.GetSecretParams{Region: reg, SecretARN: sarn})
							if err != nil {
								return err
							}
							ac.SASLUsername, ac.SASLPassword = sec.Username, sec.Password
						}
						if err := ac.Validate(); err != nil {
							return err
						}
						admin, err := newKafkaAdmin(ctx, ac)
						if err != nil {
							return err
						}
						defer admin.Close()
						acls, err := admin.ListACLs(ctx, kafka.ListACLsParams{ResourceType: rtype, ResourceName: rname, Principal: princ, Operation: oper})
						if err != nil {
							return err
						}
						rows := make([][]string, 0, len(acls))
						for _, a := range acls {
							rows = append(rows, []string{a.ResourceType, a.ResourceName, a.ResourcePattern, a.Principal, a.Host, a.Operation, a.Permission})
						}
						showTable([]string{"ResourceType", "Name", "Pattern", "Principal", "Host", "Operation", "Permission"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("ACL: List")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Item: Groups -> List
			menu.AddItem("Group: List", "", '5', func() {
				form := tview.NewForm().
					AddInputField("Brokers", defBrokers, 80, nil, nil).
					AddInputField("SASL Username", defUser, 40, nil, nil).
					AddPasswordField("SASL Password", defPass, 40, '*', nil).
					AddInputField("Secret ARN", "", 80, nil, nil).
					AddInputField("Region (for Secret)", defRegion, 40, nil, nil)
				form.AddButton("Run", func() {
					brokers := strings.TrimSpace(form.GetFormItemByLabel("Brokers").(*tview.InputField).GetText())
					u := strings.TrimSpace(form.GetFormItemByLabel("SASL Username").(*tview.InputField).GetText())
					p := strings.TrimSpace(form.GetFormItemByLabel("SASL Password").(*tview.InputField).GetText())
					sarn := strings.TrimSpace(form.GetFormItemByLabel("Secret ARN").(*tview.InputField).GetText())
					reg := strings.TrimSpace(form.GetFormItemByLabel("Region (for Secret)").(*tview.InputField).GetText())
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						if sarn == "" {
							if strings.TrimSpace(u) == "" {
								u = config.GetDefaultSASLUsername()
							}
							if strings.TrimSpace(p) == "" {
								p = config.GetDefaultSASLPassword()
							}
						}
						ac := kafka.AuthConfig{Brokers: splitNotEmpty(brokers), SASLUsername: u, SASLPassword: p, SCRAMMechanism: config.GetDefaultSCRAM()}
						if sarn != "" {
							sec, err := awsGetSecret(ctx, iaws.GetSecretParams{Region: reg, SecretARN: sarn})
							if err != nil {
								return err
							}
							ac.SASLUsername, ac.SASLPassword = sec.Username, sec.Password
						}
						if err := ac.Validate(); err != nil {
							return err
						}
						admin, err := newKafkaAdmin(ctx, ac)
						if err != nil {
							return err
						}
						defer admin.Close()
						groups, err := admin.ListConsumerGroups(ctx)
						if err != nil {
							return err
						}
						rows := make([][]string, 0, len(groups))
						for _, g := range groups {
							rows = append(rows, []string{g})
						}
						showTable([]string{"GroupID"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("Group: List")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Item: Groups -> Describe
			menu.AddItem("Group: Describe", "", '6', func() {
				form := tview.NewForm().
					AddInputField("Brokers", defBrokers, 80, nil, nil).
					AddInputField("SASL Username", defUser, 40, nil, nil).
					AddPasswordField("SASL Password", defPass, 40, '*', nil).
					AddInputField("Secret ARN", "", 80, nil, nil).
					AddInputField("Region (for Secret)", defRegion, 40, nil, nil).
					AddInputField("Group ID", "", 60, nil, nil)
				form.AddButton("Run", func() {
					brokers := strings.TrimSpace(form.GetFormItemByLabel("Brokers").(*tview.InputField).GetText())
					u := strings.TrimSpace(form.GetFormItemByLabel("SASL Username").(*tview.InputField).GetText())
					p := strings.TrimSpace(form.GetFormItemByLabel("SASL Password").(*tview.InputField).GetText())
					sarn := strings.TrimSpace(form.GetFormItemByLabel("Secret ARN").(*tview.InputField).GetText())
					reg := strings.TrimSpace(form.GetFormItemByLabel("Region (for Secret)").(*tview.InputField).GetText())
					gid := strings.TrimSpace(form.GetFormItemByLabel("Group ID").(*tview.InputField).GetText())
					runAsync(func() error {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						defer cancel()
						if sarn == "" {
							if strings.TrimSpace(u) == "" {
								u = config.GetDefaultSASLUsername()
							}
							if strings.TrimSpace(p) == "" {
								p = config.GetDefaultSASLPassword()
							}
						}
						ac := kafka.AuthConfig{Brokers: splitNotEmpty(brokers), SASLUsername: u, SASLPassword: p, SCRAMMechanism: config.GetDefaultSCRAM()}
						if sarn != "" {
							sec, err := awsGetSecret(ctx, iaws.GetSecretParams{Region: reg, SecretARN: sarn})
							if err != nil {
								return err
							}
							ac.SASLUsername, ac.SASLPassword = sec.Username, sec.Password
						}
						if err := ac.Validate(); err != nil {
							return err
						}
						admin, err := newKafkaAdmin(ctx, ac)
						if err != nil {
							return err
						}
						defer admin.Close()
						d, err := admin.DescribeConsumerGroup(ctx, gid)
						if err != nil {
							return err
						}
						// show members as table; state in status
						setStatus("Group " + d.GroupID + " — State: " + d.State)
						rows := make([][]string, 0, len(d.Members))
						for _, m := range d.Members {
							rows = append(rows, []string{m.MemberID, m.ClientID, m.ClientHost})
						}
						showTable([]string{"MemberID", "ClientID", "ClientHost"}, rows)
						return nil
					})
					app.SetRoot(layout, true)
				})
				form.AddButton("Cancel", func() { app.SetRoot(layout, true) })
				form.SetBorder(true).SetTitle("Group: Describe")
				app.SetRoot(tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(form, 0, 1, true).AddItem(status, 1, 0, false), true)
			})

			// Layout: header | content | status with left menu
			content := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(pages, 0, 1, false).AddItem(status, 1, 0, false)
			layout = tview.NewFlex().AddItem(menu, 32, 0, true).AddItem(content, 0, 1, false)
			root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(header, 1, 0, false).AddItem(layout, 0, 1, true)

			app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
				if ev.Key() == tcell.KeyRune && (ev.Rune() == 'q' || ev.Rune() == 'Q') {
					app.Stop()
					return nil
				}
				return ev
			})

			// Initial view: set directly (no QueueUpdate before Run)
			text.SetText("Choose an action from the menu on the left and press Enter…")
			pages.SwitchToPage("text")
			status.SetText("Ready")
			if err := app.SetRoot(root, true).EnableMouse(true).Run(); err != nil {
				return err
			}
			return nil
		},
	}
	return cmd
}

var layout *tview.Flex // keep to return to layout from forms

func splitNotEmpty(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}