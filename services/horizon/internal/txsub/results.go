package txsub

import (
	"context"
	"database/sql"

	"github.com/stellar/go/services/horizon/internal/db2/history"
	"github.com/stellar/go/support/errors"
	"github.com/stellar/go/xdr"
)

func txResultByHash(ctx context.Context, db HorizonDB, hash string) (history.Transaction, error) {
	hr, err := db.GetTxSubmissionResult(ctx, hash)
	if err != nil {
		if db.NoRows(err) {
			return history.Transaction{}, ErrNoResults
		}
		return history.Transaction{}, errors.Wrap(err, "could not lookup transaction by hash")
	}

	return txResultFromHistory(hr)
}

func txResultFromHistory(tx history.Transaction) (history.Transaction, error) {
	var txResult xdr.TransactionResult
	err := xdr.SafeUnmarshalBase64(tx.TxResult, &txResult)
	if err == nil {
		if !txResult.Successful() {
			err = &FailedTransactionError{
				ResultXDR: tx.TxResult,
			}
		}
	} else {
		err = errors.Wrap(err, "could not unmarshall transaction result")
	}

	return tx, err
}

// checkTxAlreadyExists uses a repeatable read transaction to look up both transaction results
// and sequence numbers. Without the repeatable read transaction it is possible that the two database
// queries execute on different ledgers. In this case, txsub can mistakenly respond with a bad_seq error
// because the first query occurs when the tx is not yet ingested and the second query occurs when the tx
// is ingested.
func checkTxAlreadyExists(ctx context.Context, db HorizonDB, hash, sourceAddress string) (history.Transaction, uint64, error) {
	err := db.BeginTx(&sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return history.Transaction{}, 0, errors.Wrap(err, "cannot start repeatable read tx")
	}
	defer db.Rollback()

	tx, err := txResultByHash(ctx, db, hash)
	if err == ErrNoResults {
		var sequenceNumbers map[string]uint64
		sequenceNumbers, err = db.GetSequenceNumbers(ctx, []string{sourceAddress})
		if err != nil {
			return tx, 0, errors.Wrapf(err, "cannot fetch sequence number for %v", sourceAddress)
		}

		num, ok := sequenceNumbers[sourceAddress]
		if !ok {
			return tx, 0, ErrNoAccount
		}
		return tx, num, ErrNoResults
	}
	return tx, 0, err
}
