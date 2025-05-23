// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
)

// handleRequest is called by proposer in reaction to `miner.Seal()`
// (this is the starting of the QBFT validation process)

// It
// - validates block proposal is not empty and number correspond to the current sequence
// - creates and send PRE-PREPARE message to other validators
func (c *Core) handleRequest(request *Request) error {
	logger := c.currentLogger(true, nil)

	logger.Info("Istanbul: handle block proposal request")

	if err := c.checkRequestMsg(request); err != nil {
		if err == errInvalidMessage {
			logger.Error("Istanbul: invalid request")
		} else {
			logger.Error("Istanbul: unexpected request", "err", err, "number", request.Proposal.Number(), "hash", request.Proposal.Hash())
		}
		return err
	}

	c.current.pendingRequest = request
	if c.state == StateAcceptRequest {
		config := c.config.GetConfig(c.current.Sequence())
		if config.EmptyBlockPeriod == 0 { // emptyBlockPeriod is not set
			// Start ROUND-CHANGE timer
			c.newRoundChangeTimer()

			// Send PRE-PREPARE message to other validators
			c.sendPreprepareMsg(request)
		} else { // emptyBlockPeriod is set
			c.newRoundMutex.Lock()
			defer c.newRoundMutex.Unlock()

			if c.newRoundTimer != nil {
				c.newRoundTimer.Stop()
				c.newRoundTimer = nil
			}

			delay := time.Duration(0)

			block, ok := request.Proposal.(*types.Block)
			if ok && len(block.Transactions()) == 0 { // if empty block
				config := c.config.GetConfig(c.current.Sequence())

				if config.EmptyBlockPeriod > config.BlockPeriod {
					log.Info("EmptyBlockPeriod detected adding delay to request", "EmptyBlockPeriod", config.EmptyBlockPeriod, "BlockTime", block.Time())
					// Because the seal has an additional delay on the block period you need to subtract it from the delay
					delay = time.Duration(config.EmptyBlockPeriod-config.BlockPeriod) * time.Second
					header := block.Header()
					// Because the block period has already been added to the time we subtract it here
					header.Time = header.Time + config.EmptyBlockPeriod - config.BlockPeriod
					request.Proposal = block.WithSeal(header)
				}
			}
			if delay > 0 {
				c.newRoundTimer = time.AfterFunc(delay, func() {
					c.newRoundTimer = nil
					// Start ROUND-CHANGE timer
					c.newRoundChangeTimer()

					// Send PRE-PREPARE message to other validators
					c.sendPreprepareMsg(request)
				})
			} else {
				// Start ROUND-CHANGE timer
				c.newRoundChangeTimer()

				// Send PRE-PREPARE message to other validators
				c.sendPreprepareMsg(request)
			}
		}
	}

	return nil
}

// check request state
// return errInvalidMessage if the message is invalid
// return errFutureMessage if the sequence of proposal is larger than current sequence
// return errOldMessage if the sequence of proposal is smaller than current sequence
func (c *Core) checkRequestMsg(request *Request) error {
	if request == nil || request.Proposal == nil {
		return errInvalidMessage
	}

	if c.current == nil {
		return errCurrentIsNil
	}

	if cmp := c.current.sequence.Cmp(request.Proposal.Number()); cmp > 0 {
		return errOldMessage
	} else if cmp < 0 {
		if new(big.Int).Add(c.current.sequence, common.Big1).Cmp(request.Proposal.Number()) < 0 {
			return errFarFutureMessage
		}
		return errFutureMessage
	} else {
		return nil
	}
}

func (c *Core) storeRequestMsg(request *Request) {
	logger := c.currentLogger(true, nil).New("proposal.number", request.Proposal.Number(), "proposal.hash", request.Proposal.Hash())

	logger.Trace("Istanbul: store block proposal request for future treatment")

	c.pendingRequestsMu.Lock()
	defer c.pendingRequestsMu.Unlock()

	c.pendingRequests.Push(request, -request.Proposal.Number().Int64())
}

// processPendingRequests is called each time QBFT state is re-initialized
// it lookup over pending requests and re-input its so they can be treated
func (c *Core) processPendingRequests() {
	c.pendingRequestsMu.Lock()
	defer c.pendingRequestsMu.Unlock()

	logger := c.currentLogger(true, nil)
	logger.Debug("Istanbul: lookup for pending block proposal requests")

	for !(c.pendingRequests.Empty()) {
		r, prio := c.pendingRequests.Pop()
		// Push back if it's a future message
		err := c.checkRequestMsg(r)
		if err != nil {
			if err == errFutureMessage {
				logger.Trace("Istanbul: stop looking up for pending block proposal request")
				c.pendingRequests.Push(r, prio)
				break
			}
			logger.Trace("Istanbul: skip pending invalid block proposal request", "number", r.Proposal.Number(), "hash", r.Proposal.Hash(), "err", err)
			continue
		}
		logger.Debug("Istanbul: found pending block proposal request", "proposal.number", r.Proposal.Number(), "proposal.hash", r.Proposal.Hash())

		go c.sendEvent(istanbul.RequestEvent{
			Proposal: r.Proposal,
		})
	}
}
