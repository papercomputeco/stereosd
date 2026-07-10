package stereosd

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ShutdownCoordinator poweroff strategy", func() {
	It("defaults to systemctl poweroff", func() {
		cmd := newMockCommander()
		sc := NewShutdownCoordinator(NewMountManager(cmd), NewLifecycleManager(), cmd, "")

		Expect(sc.Execute(context.Background(), nil)).To(Succeed())
		Expect(cmd.hasCommand("systemctl")).To(BeTrue())
	})

	It("signal-init mode signals PID 1 and never runs systemctl", func() {
		killed := false

		cmd := newMockCommander()
		sc := newShutdownCoordinatorWithKiller(
			NewMountManager(cmd),
			NewLifecycleManager(),
			cmd,
			PoweroffSignalInit,
			func() error { killed = true; return nil },
		)

		Expect(sc.Execute(context.Background(), nil)).To(Succeed())
		Expect(killed).To(BeTrue())
		Expect(cmd.hasCommand("systemctl")).To(BeFalse())
	})

	It("rejects unknown poweroff modes", func() {
		Expect(ValidatePoweroffMode("signal_init")).To(MatchError(ContainSubstring("unknown poweroff mode")))
	})
})
