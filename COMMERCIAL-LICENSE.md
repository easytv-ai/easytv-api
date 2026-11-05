# EasyTV Commercial License

**Version 1.0 — 5 November 2026**

This is the standard commercial license for EasyTV API (`easytv-api`) and,
when the Order says so, the related EasyTV repositories. It is a **separate
offer** from the [GNU Affero General Public License v3.0](LICENSE).

This file is a template. It becomes a binding license only when the Licensor
and the Licensee sign an Order (quote, invoice or signed agreement) that
points to these terms. Until then, the public AGPL-3.0 license is the only
grant.

> These terms are provided so the project can ship a dual-license from day
> one. Have counsel review them, and replace the Licensor name with the
> registered legal entity, before you sign with a customer.

---

## 1. When you need this license

EasyTV is open source under AGPL-3.0. You may use, modify and self-host it
**without** this commercial license if you follow AGPL-3.0, including its
network-copyleft rule (AGPL §13): if you offer a modified version to others
over a network, you must offer them the corresponding source.

You need a commercial license when you want to do any of the following
**without** taking on those AGPL obligations:

- Ship EasyTV, or a modified version, inside a closed-source product
- Offer EasyTV (or a fork) as a hosted / SaaS / white-label service and keep
  your modifications private
- Distribute object code or a hosted service without publishing corresponding
  source
- Remove or replace AGPL notices in **your** deployment (you still cannot
  strip third-party notices)

You do **not** need this license for personal use, research, evaluation, or
self-hosting where you comply with AGPL-3.0.

---

## 2. Parties

**Licensor** means the copyright holders of EasyTV, reachable at
[leida@easyapi.com](mailto:leida@easyapi.com). Replace this with the
registered company name on any signed Order.

**Licensee** means the legal entity named on the Order.

**Software** means the EasyTV source and binaries identified on the Order
(by default this repository, including its documentation). It does **not**
include third-party components that carry their own licenses.

**Order** means a written quote, invoice or agreement signed or accepted by
both parties that names the Licensee, the Software, the fee, the term and
any extra rights (OEM, white-label, support).

---

## 3. Grant

Subject to payment of the fees on the Order and these terms, Licensor grants
Licensee a non-exclusive, non-transferable (except as §11), worldwide license,
for the term on the Order, to:

1. Use, reproduce, modify and internally deploy the Software
2. Provide the Software, including modifications, to Licensee's own customers
   as a hosted, on-premise or white-label service
3. Combine the Software with Licensee's own code and keep that combination
   closed, without publishing corresponding source under AGPL-3.0

No other rights are granted. Title and copyright in the Software stay with
Licensor and its licensors. This license is not a sale.

---

## 4. Restrictions

Licensee shall not:

- Sublicense, resell or transfer the Software except as allowed in §3.2 or
  an OEM clause on the Order
- Publish the commercially licensed source under an open-source license as if
  Licensee were the copyright owner
- Use Licensor's names, logos or trademarks (including "EasyTV") except for
  reasonable factual attribution, unless the Order grants a trademark license
- Circumvent license keys, usage limits or similar controls if Licensor
  later ships them
- Remove copyright, license or attribution notices from third-party
  components (see §5)

---

## 5. Third-party components

This commercial license covers only material whose copyright Licensor can
grant. It does **not** relicense third-party code. Those parts stay under
their original terms, including at least:

- User, payment and object-storage modules whose structure follows
  [New API](https://github.com/QuantumNous/new-api) (derived from
  [One API](https://github.com/songquanpeng/one-api)), both AGPL-3.0.
  Those parts stay AGPL-3.0; a commercial Order does **not** relicense them.
- Go module dependencies listed in `go.mod` / `go.sum`

Licensee is responsible for complying with those licenses.

---

## 6. Fees, taxes and audit

Fees, payment schedule and currency are on the Order. Fees are non-refundable
except as required by law or as the Order says.

Fees exclude taxes. Licensee pays applicable taxes other than taxes on
Licensor's income.

If the Order is usage-based (seats, environments, revenue share), Licensor
may, on reasonable notice and no more than once per 12 months, review
Licensee's relevant records solely to verify the fee. Overpayment is
credited; underpayment is due within 30 days, plus reasonable audit cost if
the shortfall exceeds 5%.

---

## 7. Support and updates

This license does not include support, SLAs or professional services unless
the Order says so.

During a paid term Licensor will make generally available updates of the
licensed Software available to Licensee. Pre-release, beta or separately
sold modules are not included unless listed on the Order.

---

## 8. Term and termination

The term starts on the effective date of the Order and runs for the period
stated there (or perpetual if the Order says so).

Either party may terminate if the other materially breaches and does not
cure within 30 days after written notice. Licensor may terminate immediately
if Licensee infringes Licensor's intellectual property or becomes insolvent.

On termination or expiry Licensee must stop using the Software under this
license and destroy copies, except copies kept solely for legal archival.
Sections 4, 5, 9, 10, 12 and 13 survive.

Termination of this commercial license does not take away any rights Licensee
already has under AGPL-3.0 for copies received under that license.

---

## 9. Warranty disclaimer

EXCEPT AS EXPRESSLY SET OUT ON THE ORDER, THE SOFTWARE IS PROVIDED "AS IS".
LICENSOR DISCLAIMS ALL WARRANTIES, EXPRESS OR IMPLIED, INCLUDING
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NON-INFRINGEMENT.

---

## 10. Limitation of liability

TO THE MAXIMUM EXTENT PERMITTED BY LAW, LICENSOR IS NOT LIABLE FOR INDIRECT,
INCIDENTAL, SPECIAL, CONSEQUENTIAL, PUNITIVE OR LOST-PROFIT DAMAGES, EVEN IF
ADVISED OF THE POSSIBILITY.

LICENSOR'S TOTAL LIABILITY UNDER THIS LICENSE SHALL NOT EXCEED THE FEES PAID
BY LICENSEE FOR THE SOFTWARE IN THE 12 MONTHS BEFORE THE CLAIM.

Nothing here limits liability that cannot be limited under applicable law
(for example fraud, or personal injury caused by negligence).

---

## 11. Assignment

Licensee may not assign this license without Licensor's prior written
consent, except to a successor of all or substantially all of Licensee's
business that agrees in writing to these terms. Licensor may assign to an
affiliate or to a buyer of the EasyTV copyrights.

---

## 12. Confidentiality

Non-public pricing, the Order and any source delivered only under this
license are confidential. Each party will use that information only to
perform this license and will protect it with reasonable care. Obligations
last three years after disclosure, or longer for trade secrets for as long
as they remain trade secrets.

---

## 13. Governing law

These terms are governed by the laws of the People's Republic of China,
without regard to conflict-of-law rules. Courts at Licensor's domicile have
exclusive jurisdiction, unless the Order names another forum.

The English text of this file is the governing text. The
[Chinese translation](COMMERCIAL-LICENSE.zh-CN.md) is for convenience.

---

## 14. Entire agreement

The Order and this file are the entire commercial agreement for the Software.
If they conflict, the Order wins. A waiver or change is effective only if
signed by both parties. If a provision is unenforceable, the rest remains in
force.

---

## How to obtain a commercial license

Email **[leida@easyapi.com](mailto:leida@easyapi.com)** with
`[LICENSE] easytv-api` in the subject. Include:

- Legal entity name and country
- Which repositories you need (`easytv-api`, `easytv-web`, `easytv-manage`, `easytv-partner`, …)
- How you will use it (internal, SaaS, OEM / white-label)
- Expected scale (environments, seats or similar)

We will send a quote and an Order that incorporates these terms.

Community questions that are not a purchase stay in
[GitHub Discussions](https://github.com/orgs/easytv-ai/discussions) or
[SUPPORT.md](SUPPORT.md).
