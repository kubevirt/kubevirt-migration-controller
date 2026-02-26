package console

import (
	"fmt"
	"regexp"
	"time"

	v1 "kubevirt.io/api/core/v1"

	expect "github.com/google/goexpect"
	"google.golang.org/grpc/codes"

	"kubevirt.io/kubevirt/pkg/util/net/dns"
)

const (
	connectionTimeout = 10 * time.Second
	promptTimeout     = 5 * time.Second
)

// LoginToFunction represents any of the LoginTo* functions
type LoginToFunction func(*v1.VirtualMachineInstance, ...time.Duration) error

// LoginToCirros performs a console login to a Cirros base VM
func LoginToCirros(vmi *v1.VirtualMachineInstance, timeout ...time.Duration) error {
	expecter, _, err := NewExpecter(vmi, connectionTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if err := expecter.Close(); err != nil {
			fmt.Printf("error closing expecter: %v", err)
		}
	}()
	hostName := dns.SanitizeHostname(vmi)

	// Do not login, if we already logged in
	err = expecter.Send("\n")
	if err != nil {
		return err
	}
	_, _, err = expecter.Expect(regexp.MustCompile(`\$`), promptTimeout)
	if err == nil {
		return nil
	}

	b := []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: "login as 'cirros' user. default password: 'gocubsgo'. use 'sudo' for root."},
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: hostName + " login:"},
		&expect.BSnd{S: "cirros\n"},
		&expect.BExp{R: "Password:"},
		&expect.BSnd{S: "gocubsgo\n"},
		&expect.BExp{R: PromptExpression},
	}
	loginTimeout := 180 * time.Second
	if len(timeout) > 0 {
		loginTimeout = timeout[0]
	}
	_, err = expecter.ExpectBatch(b, loginTimeout)
	if err != nil {
		fmt.Printf("error expecting batch: %v", err)
		return err
	}

	err = configureConsole(expecter, true)
	if err != nil {
		return err
	}
	return nil
}

// LoginToAlpine performs a console login to an Alpine base VM
func LoginToAlpine(vmi *v1.VirtualMachineInstance, timeout ...time.Duration) error {
	expecter, _, err := NewExpecter(vmi, connectionTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if err := expecter.Close(); err != nil {
			fmt.Printf("error closing expecter: %v", err)
		}
	}()

	err = expecter.Send("\n")
	if err != nil {
		return err
	}

	hostName := dns.SanitizeHostname(vmi)

	// Do not login, if we already logged in
	b := []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: fmt.Sprintf(`(localhost|%s):~\# `, hostName)},
	}
	_, err = expecter.ExpectBatch(b, promptTimeout)
	if err == nil {
		return nil
	}

	b = []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: fmt.Sprintf(`(localhost|%s) login: `, hostName)},
		&expect.BSnd{S: "root\n"},
		&expect.BExp{R: PromptExpression},
	}
	loginTimeout := 180 * time.Second
	if len(timeout) > 0 {
		loginTimeout = timeout[0]
	}
	if _, err := expecter.ExpectBatch(b, loginTimeout); err != nil {
		fmt.Printf("error expecting batch: %v", err)
		return err
	}

	err = configureConsole(expecter, false)
	if err != nil {
		return err
	}
	return err
}

// LoginToFedora performs a console login to a Fedora base VM
func LoginToFedora(vmi *v1.VirtualMachineInstance, timeout ...time.Duration) error {
	// TODO: This is temporary workaround for issue seen in CI
	// We see that 10seconds for an initial boot is not enough
	// At the same time it seems the OS is booted within 10sec
	// We need to have a look on Running -> Booting time
	const double = 2
	expecter, _, err := NewExpecter(vmi, double*connectionTimeout)
	if err != nil {
		return err
	}
	defer func() {
		if err := expecter.Close(); err != nil {
			fmt.Printf("error closing expecter: %v", err)
		}
	}()

	err = expecter.Send("\n")
	if err != nil {
		return err
	}

	hostName := dns.SanitizeHostname(vmi)

	// Do not login, if we already logged in
	loggedInPromptRegex := fmt.Sprintf(
		`(\[fedora@(localhost|fedora|%s|%s) ~\]\$ |\[root@(localhost|fedora|%s|%s) fedora\]\# )`,
		vmi.Name, hostName, vmi.Name, hostName,
	)
	b := []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: loggedInPromptRegex},
	}
	_, err = expecter.ExpectBatch(b, promptTimeout)
	if err == nil {
		return nil
	}

	b = []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BSnd{S: "\n"},
		&expect.BCas{C: []expect.Caser{
			&expect.Case{
				// Using only "login: " would match things like "Last failed login: Tue Jun  9 22:25:30 UTC 2020 on ttyS0"
				// and in case the VM's did not get hostname form DHCP server try the default hostname
				R:  regexp.MustCompile(fmt.Sprintf(`(localhost|fedora|%s|%s) login: `, vmi.Name, hostName)),
				S:  "fedora\n",
				T:  expect.Next(),
				Rt: 10,
			},
			&expect.Case{
				R:  regexp.MustCompile(`Password:`),
				S:  "fedora\n",
				T:  expect.Next(),
				Rt: 10,
			},
			&expect.Case{
				R:  regexp.MustCompile(`Login incorrect`),
				T:  expect.LogContinue("Failed to log in", expect.NewStatus(codes.PermissionDenied, "login failed")),
				Rt: 10,
			},
			&expect.Case{
				R: regexp.MustCompile(loggedInPromptRegex),
				T: expect.OK(),
			},
		}},
		&expect.BSnd{S: "sudo su\n"},
		&expect.BExp{R: PromptExpression},
	}
	loginTimeout := 2 * time.Minute
	if len(timeout) > 0 {
		loginTimeout = timeout[0]
	}
	res, err := expecter.ExpectBatch(b, loginTimeout)
	if err != nil {
		fmt.Printf("Login attempt failed: %+v", res)
		// Try once more since sometimes the login prompt is ripped apart by asynchronous daemon updates
		if retryRes, retryErr := expecter.ExpectBatch(b, loginTimeout); retryErr != nil {
			fmt.Printf("Retried login attempt after two minutes failed: %+v", retryRes)
			return retryErr
		}
	}

	err = configureConsole(expecter, false)
	if err != nil {
		return err
	}
	return nil
}

// OnPrivilegedPrompt performs a console check that the prompt is privileged.
func OnPrivilegedPrompt(vmi *v1.VirtualMachineInstance, timeout int) bool {
	expecter, _, err := NewExpecter(vmi, connectionTimeout)
	if err != nil {
		return false
	}
	defer func() {
		if err := expecter.Close(); err != nil {
			fmt.Printf("error closing expecter: %v", err)
		}
	}()

	b := []expect.Batcher{
		&expect.BSnd{S: "\n"},
		&expect.BExp{R: PromptExpression},
	}
	_, err = expecter.ExpectBatch(b, time.Duration(timeout)*time.Second)
	if err != nil {
		fmt.Printf("error expecting batch: %v", err)
		return false
	}

	return true
}

func configureConsole(expecter expect.Expecter, shouldSudo bool) error {
	sudoString := ""
	if shouldSudo {
		sudoString = "sudo "
	}
	batch := []expect.Batcher{
		&expect.BSnd{S: "stty cols 500 rows 500\n"},
		&expect.BExp{R: PromptExpression},
		&expect.BSnd{S: "echo $?\n"},
		&expect.BExp{R: RetValueWithPrompt("0")},
		&expect.BSnd{S: fmt.Sprintf("%sdmesg -n 1\n", sudoString)},
		&expect.BExp{R: PromptExpression},
		&expect.BSnd{S: "echo $?\n"},
		&expect.BExp{R: RetValueWithPrompt("0")},
	}
	const configureConsoleTimeout = 30 * time.Second
	resp, err := expecter.ExpectBatch(batch, configureConsoleTimeout)
	if err != nil {
		fmt.Printf("error expecting batch: %v", resp)
	}
	return err
}
