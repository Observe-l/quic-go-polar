# Improvement Guidelines for Methods Section (Polar Codes)

This document provides **structured feedback** and **improvement directions** for rewriting the Polar Codes subsection of your IEEE journal paper. It integrates your own remarks and additional observations from a close reading of the draft text.

---

## 1. Academic Style and Tone
- Current writing is overly *engineering-oriented* (practical remarks, implementation details).  
- IEEE journal papers require a more **formal, theoretical, and mathematical tone**.  
- Action:
  - Avoid excessive terms like *“XOR-only,” “byte-parallel,” “practical implementation.”*  
  - Instead, emphasize the **mathematical structure**, **proof of correctness**, and **capacity-achieving property**.  
  - Reserve implementation insights for a later **Discussion/Implementation** section.

---

## 2. Encoding Description (More Formal and Detailed)
### Current Issue
- Encoding is described only briefly with mention of XOR equivalence.  
- No clear **matrix structure** or **systematic form** presented.

### Suggested Improvement
- Explicitly define the **polar generator matrix**:
  \[
  G_N = F^{\otimes n}, \quad 
  F = \begin{bmatrix} 1 & 0 \\ 1 & 1 \end{bmatrix}, \quad N=2^n.
  \]
- Define the **information set** \(\mathcal{A}\) and frozen set \(\mathcal{A}^c\).  
- For systematic polar codes, show the **block structure** of the generator:
  \[
  G_N =
  \begin{bmatrix}
    G_{\mathcal{A},\mathcal{A}} & G_{\mathcal{A},\mathcal{A}^c} \\
    G_{\mathcal{A}^c,\mathcal{A}} & G_{\mathcal{A}^c,\mathcal{A}^c}
  \end{bmatrix}.
  \]
- Encoding rule in systematic form:
  \[
  x = u_{\mathcal{A}} \, G_{\mathcal{A},\mathcal{A}}^{-1} G_N,
  \]
  where the submatrix \(G_{\mathcal{A},\mathcal{A}}\) is invertible.  
- This explicitly shows that **information bits appear directly** in the codeword (systematic property).  
- Then remark: over GF(2), this reduces to XOR operations.

---

## 3. Frozen Set Construction (Add Bhattacharyya Parameter Explanation)
### Current Issue
- Frozen set selection is glossed over, only references 3GPP tables.  
- The mathematical principle (Bhattacharyya parameter) is missing.

### Suggested Improvement
- Introduce **Bhattacharyya parameter** for channel reliability:
  \[
  Z(W) = \sum_y \sqrt{W(y|0)W(y|1)}.
  \]
- Recursive formula for polarized channels:
  \[
  Z(W^-) \leq 2Z(W) - Z(W)^2, 
  \quad Z(W^+) = Z(W)^2.
  \]
- Explain: smaller \(Z\) → more reliable channel → index placed into \(\mathcal{A}\).  
- For packet-level adaptation: treat each packet as a symbol transmitted over a binary erasure channel (BEC), where erasure probability = packet loss rate.

---

## 4. Complexity Analysis (Refocus on Polar Codes Only)
### Current Issue
- Current draft compares Polar vs RS vs RLC in the **Methods section**.  
- This shifts focus away from Polar itself and is stylistically inappropriate for Methods.

### Suggested Improvement
- Focus only on **Polar encoding/decoding complexity** here.  
- Provide **step-by-step derivations**:
  - Encoding complexity:  
    The butterfly network has \(N \log N\) edges; each edge corresponds to one XOR.  
    \[
    C_{\text{enc}}(N) = O(N \log N).
    \]
  - Decoding complexity (Gaussian elimination for packet-level erasure decoding):  
    Solving a \(K \times K\) binary linear system by Gaussian elimination requires  
    \[
    C_{\text{dec}}(K) = O(K^3) \ \text{XOR operations}.
    \]
- Support statements with references (Arıkan 2009, Tal & Vardy 2011).  
- Mention practical efficiency: XOR operations are **bitwise single-cycle** on most CPUs.

---

## 5. Clarity on GF(2), GF(256), XOR Terminology
### Current Issue
- Draft repeats GF(2), GF(256), XOR too frequently → text becomes verbose and less formal.

### Suggested Improvement
- Early in the section, state:  
  *“All operations in polar coding are carried out over GF(2), i.e., addition corresponds to bitwise XOR.”*  
- After this declaration, **do not repeat GF(2)/XOR** everywhere. Simply use “addition” or “multiplication” (implicitly in GF(2)).  
- This reduces clutter and raises academic clarity.

---

## 6. Section Reorganization (Proposed Flow)
1. **Principle of Polarization** (Arıkan’s construction, \(G_N\), frozen set).  
2. **Systematic Polar Code Encoding** (matrix form, block structure, encoding equation).  
3. **Frozen Set Selection** (Bhattacharyya parameter, theoretical foundation).  
4. **Decoding** (Gaussian elimination over GF(2), row operations).  
5. **Complexity Analysis of Polar Codes** (derive encoding and decoding complexity with formal expressions).  
6. *(Later sections can compare Polar with RS/RLC, but outside Methods).*

---

## 7. Summary of Key Improvements
- Replace engineering-style language with **formal mathematical exposition**.  
- Expand encoding description with **explicit matrix structure**.  
- Add frozen set derivation via **Bhattacharyya parameters**.  
- Focus complexity analysis on **Polar only**, with clear formulas.  
- State GF(2) once and avoid redundancy.  
- Ensure flow moves from principle → encoding → frozen set → decoding → complexity.

---

**References to Include**
- Arıkan (2009) – Channel polarization.  
- Tal & Vardy (2011) – Systematic Polar codes.  
- 3GPP TS 38.212 – Polar coding for control channels.

