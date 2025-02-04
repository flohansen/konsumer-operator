package monitor

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LagMonitor", func() {
	Context("When Start is being called", func() {
		It("should return error when executed multiple times without stopping first", func() {
			// given
			m := &LagMonitor{}

			// when
			err := make(chan error)
			go func() {
				err <- m.Start(context.Background(), "group", "topic")
			}()
			go func() {
				err <- m.Start(context.Background(), "group", "topic")
			}()

			// then
			Expect(<-err).To(Equal(ErrAlreadyStarted))
		})

		It("should return error if context has been canceled", func() {
			// given
			ctx, cancel := context.WithCancel(context.Background())
			m := &LagMonitor{}

			// when
			err := make(chan error)
			go func() {
				err <- m.Start(ctx, "group", "topic")
			}()
			cancel()

			// then
			Expect(<-err).To(Equal(context.Canceled))
		})

		It("should return error if monitor has been stopped", func() {
			// given
			ctx := context.Background()
			m := &LagMonitor{}

			// when
			init := make(chan bool)
			err := make(chan error)

			go func() {
				close(init)
				err <- m.Start(ctx, "group", "topic")
			}()

			<-init
			m.Stop()

			// then
			Expect(<-err).To(Equal(context.Canceled))
		})
	})
})
